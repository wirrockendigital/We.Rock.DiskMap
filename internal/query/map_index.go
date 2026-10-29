package query

import (
	"context"
	"database/sql"
	"sort"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// MapIdentity carries only bounded virtual display metadata; real names use the detail API.
type MapIdentity struct {
	Slot uint64 `json:"slot"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// MapIndexIdentities resolves the selected root through the canonical captured source topology.
func MapIndexIdentities(ctx context.Context, db *sql.DB, h index.GenerationHeader, root string, r interface {
	Slot(context.Context, string) (uint64, error)
}) (string, []MapIdentity, error) {
	e, err := virtualSelectionEngine(ctx, db, h)
	if err != nil {
		return "", nil, err
	}
	e.r.RootID = root
	n, err := e.root()
	if err != nil {
		return "", nil, err
	}
	ids := []MapIdentity{}
	keys := make([]string, 0, len(e.roots))
	for id, v := range e.roots {
		if v.node.Virtual {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	for _, id := range keys {
		v := e.roots[id]
		slot, err := r.Slot(ctx, id)
		if err != nil {
			return "", nil, err
		}
		ids = append(ids, MapIdentity{slot, id, v.node.Name, v.node.Kind})
	}
	return n.node.ID, ids, nil
}

// ValidateMapIndexRoot applies canonical root qualification without rebuilding manifest identities per block batch.
func ValidateMapIndexRoot(ctx context.Context, db *sql.DB, h index.GenerationHeader, root string) error {
	e, err := virtualSelectionEngine(ctx, db, h)
	if err != nil {
		return err
	}
	e.r.RootID = root
	_, err = e.root()
	return err
}

// FeedMapIndex projects the same disjoint source topology as navigation into a disk-backed tree.
// Only bounded virtual source metadata is retained; actual file rows are consumed sequentially.
func FeedMapIndex(ctx context.Context, db *sql.DB, h index.GenerationHeader, b interface {
	AddVisualNode(contracts.VisualIndexNode) error
}) error {
	e, err := virtualSelectionEngine(ctx, db, h)
	if err != nil {
		return err
	}
	added := map[string]bool{}
	visiting := map[string]bool{}
	var virtual func(string) error
	virtual = func(id string) error {
		v, ok := e.roots[id]
		if !ok || !v.node.Virtual || added[id] {
			return nil
		}
		if visiting[id] {
			return ErrSnapshot
		}
		visiting[id] = true
		parent := ""
		if v.node.ParentID != nil {
			parent = *v.node.ParentID
			if err := virtual(parent); err != nil {
				return err
			}
		}
		if err := b.AddVisualNode(contracts.VisualIndexNode{ID: id, Parent: parent, Kind: 4}); err != nil {
			return err
		}
		added[id] = true
		delete(visiting, id)
		return nil
	}
	if err = virtual(virtualID(h.SnapshotID, "nas", "")); err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,parent_id,name_bytes,kind,logical_bytes FROM nodes ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var parent sql.NullInt64
		var name []byte
		var kind string
		var size sql.NullString
		if err = rows.Scan(&id, &parent, &name, &kind, &size); err != nil {
			break
		}
		in := contracts.VisualIndexNode{ID: strconv.FormatInt(id, 10), Source: uint64(id), Flags: mapFlags(string(name))}
		if parent.Valid {
			in.Parent = strconv.FormatInt(parent.Int64, 10)
		}
		if p, ok := e.parents[in.ID]; ok {
			in.Parent = p
		}
		if err = virtual(in.Parent); err != nil {
			break
		}
		switch kind {
		case "directory":
			in.Kind = 2
		case "file":
			in.Kind = 1
		default:
			in.Kind = 3
		}
		if in.Kind == 1 {
			in.Unknown = !size.Valid
			if size.Valid {
				in.Bytes = size.String
			}
		}
		if err = b.AddVisualNode(in); err != nil {
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Empty source containers remain addressable even when they have no measured descendants.
	keys := make([]string, 0, len(e.roots))
	for id := range e.roots {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, id := range keys {
		if err = virtual(id); err != nil {
			return err
		}
	}
	return nil
}
