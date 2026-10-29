package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// record carries only bounded index metadata and the stable insertion key used by continuation.
type record struct {
	node   Node
	row    int64
	direct Summary
}
type engine struct {
	sourceReady   bool
	projected     map[string]record
	replacements  map[string]record
	excluded      map[string]map[string]bool
	containers    map[string]string
	parents       map[string]string
	ctx           context.Context
	db            *sql.DB
	projection    *sql.DB
	h             index.GenerationHeader
	r             Request
	virtual       map[string][]record
	roots         map[string]record
	rows          int
	bufferLimited bool
	timeLimited   bool
	expandUntil   time.Time
	now           func() time.Time
}

// expansionExpired stops optional work while preserving the hard context deadline for all operations.
func (e *engine) expansionExpired() bool {
	return e.now != nil && !e.expandUntil.IsZero() && !e.now().Before(e.expandUntil)
}

// decodeNode consumes the native DTO without a second JSON serialization of every tree entry.
func (e *engine) decodeNode(n contracts.Node, name, rawPath []byte, aggregate sql.NullString, row int64) (record, error) {
	if n.SnapshotID != e.r.SnapshotID || contracts.ValidateNodeType(n) != nil {
		return record{}, ErrSnapshot
	}
	info, err := classifyRecorded(n, name, rawPath, aggregate, e.h.FileTypeMapping)
	if err != nil {
		return record{}, err
	}
	out := record{node: Node{Classification: &info, ID: n.ID, ParentID: n.ParentID, RealNodeID: &n.ID, Name: n.OriginalName, ModifiedAt: nil, Kind: n.Kind, VolumeID: n.VolumeID, ShareID: n.ShareID, Summary: zero()}, row: row, direct: zero()}
	if n.Kind == "directory" {
		var a contracts.DirectoryAggregate
		if !aggregate.Valid || len(aggregate.String) > 32768 || json.Unmarshal([]byte(aggregate.String), &a) != nil {
			return out, ErrSnapshot
		}
		var err error
		out.node.Summary, err = content(a.Subtree, Request{Metric: e.r.Metric})
		if err != nil {
			return out, err
		}
		out.direct, err = content(a.Direct, Request{Metric: e.r.Metric})
		if err != nil {
			return out, err
		}
		out.node.Summary.DirectoryCount, err = arithmetic(out.node.Summary.DirectoryCount, "1", false)
		if err != nil {
			return out, err
		}
		out.node.HasMore = count(out.direct) != "0"
	} else if n.Kind == "file" {
		out.node.Summary.FileCount = "1"
		v := n.LogicalBytes
		if e.r.Metric == "allocated" {
			v = n.AllocatedBytes
		}
		if v == nil {
			out.node.Summary.UnknownFileCount = "1"
		} else {
			out.node.Summary.KnownBytes = *v
		}
	} else if n.Kind != "file" {
		out.node.Summary.SpecialCount = "1"
	}
	out.node.Summary, err = merge(zero(), out.node.Summary, false)
	if err != nil {
		return out, err
	}
	return e.applyFilter(out)
}

// decodeRecord consumes only the current native tree reader and its bounded aggregate.
func (e *engine) decodeRecord(v index.NodeRecord) (record, error) {
	var aggregate sql.NullString
	if v.Node.Kind == "directory" {
		a, err := index.ReadDirectoryAggregate(e.ctx, e.db, v.Node.ID)
		if err != nil {
			return record{}, err
		}
		raw, err := json.Marshal(a)
		if err != nil {
			return record{}, err
		}
		aggregate = sql.NullString{String: string(raw), Valid: true}
	}
	out, err := e.decodeNode(v.Node, v.Name, v.Path, aggregate, v.RowID)
	if err == nil {
		out.node.ColorBranchKey = e.colorBranchKeyForTarget(v.Node, v.Path, v.TargetID)
	}
	return out, err
}

// rawReal resolves one snapshot-bound numeric locator without source filesystem access.
func (e *engine) rawReal(id string) (record, error) {
	v, err := index.ReadNode(e.ctx, e.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return record{}, ErrRoot
	}
	if err != nil {
		return record{}, err
	}
	e.rows++
	return e.decodeRecord(v)
}

// children uses the existing parent index's rowid ordering, with a hard physical row cap.
func (e *engine) children(parent string, after int64, limit int, initial bool) ([]record, bool, int64, error) {
	e.bufferLimited = false
	e.timeLimited = false
	if v, ok := e.virtual[parent]; ok {
		start := int(after)
		if start < 0 || start > len(v) {
			return nil, false, after, ErrRequest
		}
		end := start + limit
		if end > len(v) {
			end = len(v)
		}
		return v[start:end], end < len(v), int64(end), nil
	}
	if initial {
		after = 0
	}
	out := make([]record, 0, limit)
	seen := 0
	bufferBytes := 0
	inputBytes := 0
	// Fetch bounded physical batches without lowering the caller's logical page budget.
	for {
		pageSize := min(128, limit+1-seen)
		nodes, err := index.Children(e.ctx, e.db, parent, after, pageSize)
		if err != nil {
			return nil, false, after, err
		}
		for _, stored := range nodes {
			// Always allow one bounded row for standalone paging to avoid a stalled cursor.
			if seen > 0 && e.expansionExpired() {
				e.timeLimited = true
				return out, true, after, nil
			}
			e.rows++
			if seen == limit {
				return out, true, after, nil
			}
			seen++
			row := stored.RowID
			encodedNode, _ := json.Marshal(stored.Node)
			raw, name, rawPath := string(encodedNode), stored.Name, stored.Path
			inputCost := len(raw) + len(name) + len(rawPath)
			if inputBytes > 0 && inputBytes+inputCost > MaxResponseBytes {
				e.bufferLimited = true
				return out, true, after, nil
			}
			inputBytes += inputCost
			v, er := e.decodeRecord(stored)
			if er != nil {
				return nil, false, after, er
			}
			if e.excluded[parent][v.node.ID] {
				after = row
				continue
			}
			if replacement, ok := e.replacements[v.node.ID]; ok {
				replacement.row = row
				v = replacement
			}
			if count(v.node.Summary) != "0" {
				// Bound retained row payloads before accumulating a whole requested page.
				// Stop before this row so continuation cannot silently drop the first unbuffered child.
				encoded, _ := json.Marshal(v.node)
				cost := len(raw) + len(name) + len(encoded)
				if len(out) > 0 && bufferBytes+cost > MaxResponseBytes {
					e.bufferLimited = true
					return out, true, after, nil
				}
				if cost > MaxResponseBytes {
					return nil, false, after, ErrBudget
				}
				bufferBytes += cost
				out = append(out, v)
			}
			after = row
		}
		if len(nodes) < pageSize {
			return out, false, after, nil
		}
	}
}

// virtualID cannot alias a canonical node generated by the scanner and remains snapshot-specific.
func virtualID(snapshot, kind, key string) string {
	return fmt.Sprintf("q-%s-%x", kind, sha256.Sum256([]byte(snapshot+"\x00"+key)))
}

// topology builds only the bounded manifest roots, never descendants or source directory listings.
func (e *engine) baseTopology() error {
	e.virtual = map[string][]record{}
	e.roots = map[string]record{}
	nodes, err := index.RootNodes(e.ctx, e.db)
	if err != nil {
		return err
	}
	roots := []record{}
	volumes := map[string]string{}
	for _, stored := range nodes {
		if len(roots) >= 1024 {
			return ErrBudget
		}
		v, err := e.decodeRecord(stored)
		if err != nil {
			return err
		}
		volumes[v.node.ID] = stored.Node.VolumeID
		roots = append(roots, v)
	}
	nas := record{node: Node{ID: virtualID(e.r.SnapshotID, "nas", ""), Name: "NAS", Kind: "nas", Virtual: true, Summary: zero(), OriginalSummary: zero(), MatchSummary: zero(), MatchKind: "ancestor"}, direct: zero()}
	volumeOrder := []string{}
	for _, r := range roots {
		volume := volumes[r.node.ID]
		id := virtualID(e.r.SnapshotID, "volume", volume)
		v, exists := e.roots[id]
		if !exists {
			v = record{node: Node{ColorBranchKey: e.volumeColorKey(volume), ID: id, ParentID: &nas.node.ID, Name: volume, Kind: "volume", VolumeID: volume, Virtual: true, Summary: zero(), OriginalSummary: zero(), MatchSummary: zero(), MatchKind: "ancestor"}, direct: zero()}
			volumeOrder = append(volumeOrder, id)
		}
		r.node.ParentID = &id
		e.virtual[id] = append(e.virtual[id], r)
		v.node.Summary, err = merge(v.node.Summary, r.node.Summary, false)
		v.node.OriginalSummary, err = merge(v.node.OriginalSummary, r.node.OriginalSummary, false)
		if err != nil {
			return err
		}
		v.node.MatchSummary, err = merge(v.node.MatchSummary, r.node.MatchSummary, false)
		if err != nil {
			return err
		}
		v.node.HasMore = len(e.virtual[id]) > 0
		e.roots[id] = v
	}
	for _, id := range volumeOrder {
		v := e.roots[id]
		e.virtual[nas.node.ID] = append(e.virtual[nas.node.ID], v)
		nas.node.Summary, err = merge(nas.node.Summary, v.node.Summary, false)
		nas.node.OriginalSummary, err = merge(nas.node.OriginalSummary, v.node.OriginalSummary, false)
		if err != nil {
			return err
		}
		nas.node.MatchSummary, err = merge(nas.node.MatchSummary, v.node.MatchSummary, false)
		if err != nil {
			return err
		}
	}
	nas.node.HasMore = len(e.virtual[nas.node.ID]) > 0
	e.roots[nas.node.ID] = nas
	return nil
}

// root accepts the explicit NAS selector or one qualified node; virtual groups drill through ListRootID.
func (e *engine) root() (record, error) {
	if err := e.topology(); err != nil {
		return record{}, err
	}
	if e.r.RootID == "nas" || len(e.r.RootID) > 2 && e.r.RootID[:2] == "q-" {
		if err := e.topology(); err != nil {
			return record{}, err
		}
		id := e.r.RootID
		if id == "nas" {
			id = virtualID(e.r.SnapshotID, "nas", "")
		}
		if v, ok := e.roots[id]; ok {
			return v, nil
		}
		return record{}, ErrRoot
	}
	return e.real(e.r.RootID)
}
