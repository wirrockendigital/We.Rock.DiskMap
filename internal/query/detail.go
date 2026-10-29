package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

const MaxDetailResponseBytes = 256 << 10

var ErrDetailUnavailable = errors.New("DETAIL_UNAVAILABLE")
var ErrDetailMetric = errors.New("METRIC_UNAVAILABLE")

// DetailRequest never accepts paths or current source metadata.
type DetailRequest struct {
	SnapshotID string `json:"snapshotId"`
	RootID     string `json:"rootId"`
	NodeID     string `json:"nodeId"`
	Metric     string `json:"metric"`
	Operation  string `json:"operation"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// Validate prevents ignored ranking selectors on ordinary detail reads.
func (r DetailRequest) Validate() error {
	if _, e := (Request{SnapshotID: r.SnapshotID, RootID: r.RootID, Metric: r.Metric, Depth: 1, ElementBudget: 1}).Validate(); e != nil {
		return ErrRequest
	}
	if r.NodeID == "" || len(r.NodeID) > 160 {
		return ErrRequest
	}
	switch r.Operation {
	case "details":
		if r.Cursor != "" || r.Limit != 0 {
			return ErrRequest
		}
	case "direct-folders", "recursive-files":
		if r.Limit < 1 || r.Limit > 20 || len(r.Cursor) > MaxSearchCursorBytes {
			return ErrRequest
		}
	default:
		return ErrRequest
	}
	return nil
}

// DetailAggregate uses null direct content for virtual containers whose immediate children are virtual.
type DetailAggregate struct {
	Direct          *contracts.ContentAggregate `json:"direct"`
	Subtree         contracts.ContentAggregate  `json:"subtree"`
	DirectSemantics string                      `json:"directSemantics"`
}

// DetailResponse separates immutable metadata, exact count bases and selected-metric ranking pages.
type DetailResponse struct {
	SourceAvailability SourceAvailability     `json:"sourceAvailability"`
	Provenance         *SourceProvenance      `json:"provenance,omitempty"`
	SnapshotID         string                 `json:"snapshotId"`
	RootID             string                 `json:"rootId"`
	NodeID             string                 `json:"nodeId"`
	Operation          string                 `json:"operation"`
	Metric             string                 `json:"metric"`
	Kind               string                 `json:"kind"`
	Name               string                 `json:"name"`
	Virtual            bool                   `json:"virtual"`
	Node               *contracts.Node        `json:"node"`
	GroupID            *string                `json:"groupId"`
	Classification     *Classification        `json:"classification"`
	Aggregate          *DetailAggregate       `json:"aggregate"`
	Numerator          Summary                `json:"numerator"`
	Denominator        Summary                `json:"denominator"`
	Basis              string                 `json:"basis"`
	Logical            SizeMetric             `json:"logical"`
	Allocated          SizeMetric             `json:"allocated"`
	Capture            SizeCapture            `json:"capture"`
	ProfileRevision    int                    `json:"profileRevision"`
	SnapshotState      string                 `json:"snapshotState"`
	Coverage           []index.CoverageRecord `json:"coverage"`
	CoverageTruncated  bool                   `json:"coverageTruncated"`
	BoundaryCount      contracts.Bytes        `json:"boundaryCount"`
	Ranking            *SearchPage            `json:"ranking"`
	RankingSemantics   string                 `json:"rankingSemantics"`
}

// Detail qualifies ancestry in the same snapshot before returning any recorded metadata.
func Detail(parent context.Context, db *sql.DB, h index.GenerationHeader, r DetailRequest) (DetailResponse, error) {
	var out DetailResponse
	if r.Validate() != nil {
		return out, ErrRequest
	}
	// Bind the pure query before any lookup, independently of manager and bridge guards.
	if r.SnapshotID != h.SnapshotID {
		return out, ErrSnapshot
	}
	ctx, cancel := context.WithTimeout(parent, QueryTimeout)
	defer cancel()
	if strings.HasPrefix(r.NodeID, "q-group-") {
		return out, ErrDetailUnavailable
	}
	selected := r.NodeID
	if selected == "nas" {
		selected = virtualID(h.SnapshotID, "nas", "")
	}
	nav, e := ReadNavigation(ctx, db, h, r.RootID, &selected)
	if e != nil {
		return out, e
	}
	root := r.RootID
	if root == "nas" {
		root = virtualID(h.SnapshotID, "nas", "")
	}
	if nav.RootID != root || nav.SelectedNodeID == nil || *nav.SelectedNodeID != selected {
		return out, ErrRoot
	}
	engineFor := func(metric, id string) *engine {
		return &engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: r.SnapshotID, RootID: id, Metric: metric, Depth: 1, ElementBudget: 1, Filter: Filter{Categories: []string{}}}, now: time.Now}
	}
	logical, err := engineFor("logical", selected).root()
	if err != nil {
		return out, err
	}
	allocated, err := engineFor("allocated", selected).root()
	if err != nil {
		return out, err
	}
	denom, err := engineFor(r.Metric, root).root()
	if err != nil {
		return out, err
	}
	metrics, err := sizeMetrics(h, logical.node, allocated.node, "", nil)
	if err != nil {
		return out, err
	}
	if r.Metric == "allocated" && !metrics.Allocated.Selectable {
		return out, ErrDetailMetric
	}
	out = DetailResponse{SnapshotID: h.SnapshotID, RootID: root, NodeID: selected, Operation: r.Operation, Metric: r.Metric, Provenance: logical.node.Provenance, Kind: logical.node.Kind, Name: logical.node.Name, Virtual: logical.node.Virtual, Classification: logical.node.Classification, Numerator: logical.node.Summary, Denominator: denom.node.Summary, Basis: "unfiltered-recorded-path-entries", Logical: metrics.Logical, Allocated: metrics.Allocated, Capture: metrics.Capture, ProfileRevision: h.Profile.Revision, SnapshotState: h.State, Coverage: []index.CoverageRecord{}, BoundaryCount: h.ScanBoundaryCount, RankingSemantics: "not-requested"}
	out.SourceAvailability, err = engineFor("logical", selected).sourceAvailability(selected)
	if err != nil {
		return DetailResponse{}, err
	}
	if r.Metric == "allocated" {
		out.Numerator = allocated.node.Summary
	}
	// Coverage remains generation-level evidence; a missing node is never fabricated as a zero-byte file.
	for _, c := range h.Coverage {
		if len(out.Coverage) == 32 {
			out.CoverageTruncated = true
			break
		}
		out.Coverage = append(out.Coverage, c)
	}
	if !out.Virtual {
		stored, readErr := index.ReadNode(ctx, db, selected)
		if readErr != nil || stored.Node.SnapshotID != h.SnapshotID {
			return DetailResponse{}, ErrSnapshot
		}
		n := stored.Node
		out.Node = &n
		if n.Kind == "directory" {
			a, e := index.ReadDirectoryAggregate(ctx, db, selected)
			if e != nil {
				return DetailResponse{}, e
			}
			projector := engineFor("logical", selected)
			if e := projector.topology(); e != nil {
				return DetailResponse{}, e
			}
			for id := range projector.excluded[selected] {
				child, e := index.ReadDirectoryAggregate(ctx, db, id)
				if e != nil {
					return DetailResponse{}, e
				}
				v := child.Subtree
				v.DirectoryCount, e = arithmetic(v.DirectoryCount, "1", false)
				if e != nil {
					return DetailResponse{}, e
				}
				if e = combineDetailContent(&a.Subtree, v, true); e != nil {
					return DetailResponse{}, e
				}
				a.Direct.DirectoryCount, e = arithmetic(a.Direct.DirectoryCount, "1", true)
				if e != nil {
					return DetailResponse{}, e
				}
			}
			out.Aggregate = &DetailAggregate{Direct: &a.Direct, Subtree: a.Subtree, DirectSemantics: "immediate-real-children"}
		}
	} else {
		a, e := projectedDetailAggregate(ctx, db, h, selected)
		if e != nil {
			return DetailResponse{}, e
		}
		out.Aggregate = &DetailAggregate{Subtree: a, DirectSemantics: "virtual-children-not-file-content"}
	}
	if r.Operation != "details" {
		if out.Kind != "directory" && out.Kind != "nas" && out.Kind != "volume" && out.Kind != "share" && out.Kind != "other" {
			return DetailResponse{}, ErrDetailUnavailable
		}
		scope, kind := "direct", "directory"
		out.RankingSemantics = "direct-real-folders-disjoint-subtrees"
		if r.Operation == "recursive-files" {
			scope, kind = "recursive", "file"
			out.RankingSemantics = "recursive-file-path-entries-not-exclusive-physical-bytes"
		}
		after, e := detailCursorAfter(r)
		if e != nil {
			return DetailResponse{}, e
		}
		page, e := Search(ctx, db, h, SearchRequest{SnapshotID: h.SnapshotID, RootID: selected, Scope: scope, Field: "both", Metric: r.Metric, Sort: "size", Direction: "desc", Limit: r.Limit, Filter: Filter{ObjectKinds: []string{kind}}}, after)
		if e != nil {
			return DetailResponse{}, e
		}
		if page.NextCursor != "" {
			page.NextCursor = detailCursor(r, page.NextCursor)
		}
		out.Ranking = &page
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > MaxDetailResponseBytes-4096 {
		return DetailResponse{}, ErrBudget
	}
	return out, nil
}

// virtualDetailAggregate sums only disjoint captured scope roots, including their directory entries.
func virtualDetailAggregate(ctx context.Context, db *sql.DB, h index.GenerationHeader, kind, volume string) (contracts.ContentAggregate, error) {
	out := contracts.ContentAggregate{FileCount: "0", DirectoryCount: "0", SpecialCount: "0", Logical: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}, Allocated: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}, Types: map[string]contracts.TypeAggregate{}}
	nodes, e := index.RootNodes(ctx, db)
	if e != nil {
		return out, e
	}
	if len(nodes) > 1024 {
		return out, ErrBudget
	}
	for _, stored := range nodes {
		n := stored.Node
		if n.SnapshotID != h.SnapshotID {
			return out, ErrSnapshot
		}
		if kind == "volume" && n.VolumeID != volume {
			continue
		}
		a, err := index.ReadDirectoryAggregate(ctx, db, n.ID)
		if err != nil {
			return out, err
		}
		v := a.Subtree
		v.DirectoryCount, e = arithmetic(v.DirectoryCount, "1", false)
		if e != nil {
			return out, e
		}
		if e = mergeDetailContent(&out, v); e != nil {
			return out, e
		}
	}
	return out, nil
}

// mergeDetailContent performs exact decimal arithmetic for every count, metric and sparse type bucket.
func mergeDetailContent(out *contracts.ContentAggregate, v contracts.ContentAggregate) error {
	return combineDetailContent(out, v, false)
}

// combineDetailContent also supports exact subtraction of disjoint recorded source boundaries.
func combineDetailContent(out *contracts.ContentAggregate, v contracts.ContentAggregate, subtract bool) error {
	add := func(a *contracts.Bytes, b contracts.Bytes) error {
		x, e := arithmetic(*a, b, subtract)
		if e == nil {
			*a = x
		}
		return e
	}
	for _, p := range []struct {
		a *contracts.Bytes
		b contracts.Bytes
	}{{&out.FileCount, v.FileCount}, {&out.DirectoryCount, v.DirectoryCount}, {&out.SpecialCount, v.SpecialCount}, {&out.Logical.KnownBytes, v.Logical.KnownBytes}, {&out.Logical.UnknownFileCount, v.Logical.UnknownFileCount}, {&out.Allocated.KnownBytes, v.Allocated.KnownBytes}, {&out.Allocated.UnknownFileCount, v.Allocated.UnknownFileCount}} {
		if e := add(p.a, p.b); e != nil {
			return e
		}
	}
	for k, v := range v.Types {
		if !categories[k] {
			return ErrSnapshot
		}
		a, ok := out.Types[k]
		if !ok {
			a = contracts.TypeAggregate{FileCount: "0", Logical: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}, Allocated: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}}
		}
		for _, p := range []struct {
			a *contracts.Bytes
			b contracts.Bytes
		}{{&a.FileCount, v.FileCount}, {&a.Logical.KnownBytes, v.Logical.KnownBytes}, {&a.Logical.UnknownFileCount, v.Logical.UnknownFileCount}, {&a.Allocated.KnownBytes, v.Allocated.KnownBytes}, {&a.Allocated.UnknownFileCount, v.Allocated.UnknownFileCount}} {
			if e := add(p.a, p.b); e != nil {
				return e
			}
		}
		out.Types[k] = a
	}
	return nil
}

// The outer detail root is part of continuation identity, not only the selected ranking directory.
func detailCursorKey(r DetailRequest) string {
	r.Cursor = ""
	r.Limit = 0
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}
func detailCursor(r DetailRequest, after string) string {
	raw, _ := json.Marshal(struct {
		Key   string `json:"key"`
		After string `json:"after"`
	}{detailCursorKey(r), after})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func detailCursorAfter(r DetailRequest) (string, error) {
	if r.Cursor == "" {
		return "", nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(r.Cursor)
	if e != nil {
		return "", ErrRequest
	}
	var value struct {
		Key   string `json:"key"`
		After string `json:"after"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Key != detailCursorKey(r) || len(value.After) > MaxSearchCursorBytes {
		return "", ErrRequest
	}
	return value.After, nil
}

// ValidDetailResponse validates echoed identities and relations before an authenticated bridge emits data.
func ValidDetailResponse(d DetailResponse, r DetailRequest) bool {
	root, node := r.RootID, r.NodeID
	if root == "nas" {
		root = virtualID(r.SnapshotID, "nas", "")
	}
	if node == "nas" {
		node = virtualID(r.SnapshotID, "nas", "")
	}
	if d.SnapshotID != r.SnapshotID || d.RootID != root || d.NodeID != node || d.Operation != r.Operation || d.Metric != r.Metric || d.ProfileRevision < 1 || d.Basis != "unfiltered-recorded-path-entries" || (d.SnapshotState != "complete" && d.SnapshotState != "partial") {
		return false
	}
	if _, e := merge(zero(), d.Numerator, false); e != nil {
		return false
	}
	if _, e := merge(zero(), d.Denominator, false); e != nil {
		return false
	}
	if d.Provenance != nil && !ValidSourceProvenance(d.Provenance) {
		return false
	}
	if (d.Kind == "share" || d.Kind == "other") && d.Provenance == nil {
		return false
	}
	if d.Virtual {
		if (d.Kind != "nas" && d.Kind != "volume" && d.Kind != "share" && d.Kind != "other") || d.Node != nil || d.Aggregate == nil || d.Aggregate.Direct != nil {
			return false
		}
	} else {
		if d.Node == nil || d.Node.ID != node || d.Node.SnapshotID != r.SnapshotID || d.Node.Kind != d.Kind || contracts.ValidateNodeType(*d.Node) != nil {
			return false
		}
		if (d.Kind == "directory") != (d.Aggregate != nil) {
			return false
		}
	}
	if d.Allocated.Selectable && (d.Allocated.Qualification == nil || *d.Allocated.Qualification != AllocatedQualification || d.Allocated.Method == nil || *d.Allocated.Method != "st_blocks*512") {
		return false
	}
	if r.Metric == "allocated" && !d.Allocated.Selectable {
		return false
	}
	if r.Operation == "details" {
		return d.Ranking == nil && d.RankingSemantics == "not-requested"
	}
	p := d.Ranking
	if p == nil || p.SnapshotID != r.SnapshotID || p.RootID != node || p.Metric != r.Metric || p.SelectionOrder != "size-desc-recorded-rowid" || len(p.Items) > r.Limit || p.HasMore != (p.NextCursor != "") || p.WorkLimited && !p.HasMore {
		return false
	}
	kind, scope := "directory", "direct"
	if r.Operation == "recursive-files" {
		kind, scope = "file", "recursive"
	}
	if p.Scope != scope {
		return false
	}
	for _, v := range p.Items {
		if v.SnapshotID != r.SnapshotID || v.Kind != kind {
			return false
		}
	}
	return true
}
