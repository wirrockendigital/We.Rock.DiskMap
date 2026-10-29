package query

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"
)

type searchCandidate struct {
	row    int64
	id     string
	parent *string
	target string
	path   []byte
	key    []byte
}

// candidateBytes accounts for retained variable-width data and fixed candidate overhead.
func candidateBytes(v searchCandidate) int {
	return len(v.id) + len(v.target) + len(v.path) + len(v.key) + 256
}

// candidateLess defines one global order including a stable numeric-locator tie breaker.
func candidateLess(a, b searchCandidate, desc bool) bool {
	n := bytes.Compare(a.key, b.key)
	if n == 0 {
		if desc {
			return a.row > b.row
		}
		return a.row < b.row
	}
	if desc {
		return n > 0
	}
	return n < 0
}

// candidateKey preserves exact decimal and Unicode ordering without persistent search copies.
func candidateKey(r SearchRequest, item SearchItem) ([]byte, error) {
	key, err := candidateValueKey(r, item)
	if r.FoldersFirst {
		// Reverse the rank for descending order so directories always remain first.
		rank := byte(1)
		if item.Kind == "directory" {
			rank = 0
		}
		if r.Direction == "desc" {
			rank = 1 - rank
		}
		key = append([]byte{rank}, key...)
	}
	return key, err
}

// candidateValueKey orders files by their recorded category when requested.
func candidateValueKey(r SearchRequest, item SearchItem) ([]byte, error) {
	if r.CategorySort {
		value := item.Kind
		if item.Classification != nil && item.Classification.Category != nil {
			value = *item.Classification.Category
		}
		return append([]byte{1}, []byte(value+"\x00"+index.SearchFold(item.Name))...), nil
	}
	switch r.Sort {
	case "name":
		return append([]byte{1}, []byte(index.SearchFold(item.Name))...), nil
	case "type":
		return append([]byte{1}, []byte(item.Kind)...), nil
	default:
		if item.Size == nil {
			return []byte{0}, nil
		}
		v := *item.Size
		if v.Validate() != nil || len(v) > 128 {
			return nil, ErrSnapshot
		}
		return append([]byte{1, byte(len(v) >> 8), byte(len(v))}, []byte(v)...), nil
	}
}

// Search visits the numeric tree in bounded pages with the index reader's bounded path cache.
// Only the global top page is retained; an incomplete traversal never returns prefix results.
func Search(ctx context.Context, db *sql.DB, h index.GenerationHeader, request SearchRequest, after string) (SearchPage, error) {
	r, err := request.Validate()
	if err != nil {
		return SearchPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if err = checkHeader(ctx, db, h, Request{SnapshotID: r.SnapshotID}); err != nil {
		return SearchPage{}, err
	}
	scope, err := resolveSearchScope(ctx, db, h, r)
	if err != nil {
		return SearchPage{}, err
	}
	reader, err := index.NewNodeReader(ctx, db)
	if err != nil {
		return SearchPage{}, err
	}
	_, cursorRow, err := decodeSearchCursor(r, after)
	if err != nil {
		return SearchPage{}, err
	}
	desc := r.Direction == "desc"
	boundary := searchCandidate{row: cursorRow}
	if after != "" {
		stored, e := reader.Read(strconv.FormatInt(cursorRow, 10))
		if e != nil {
			return SearchPage{}, ErrSearch
		}
		boundary = searchCandidate{row: stored.RowID, id: stored.Node.ID, parent: stored.Node.ParentID, target: stored.TargetID, path: stored.Path}
		if !scope.accepts(boundary) {
			return SearchPage{}, ErrSearch
		}
		item, e := searchOrderItem(ctx, db, h, r, stored, scope.projection)
		if e != nil {
			return SearchPage{}, e
		}
		boundary.key, err = candidateKey(r, item)
		if err != nil {
			return SearchPage{}, err
		}
	}
	best := []searchCandidate{}
	var last int64
	needle := index.SearchFold(r.Text)
	if r.Filter.Text == "" || r.Filter.Field == "name" {
		best, err = searchNumericNames(ctx, db, h, r, scope, boundary, after != "", needle)
		if err != nil {
			return SearchPage{}, err
		}
	} else {
		for {
			batch, next, exhausted, e := searchReadBatch(ctx, db, reader, r, needle, last)
			if e != nil {
				if ctx.Err() != nil {
					return SearchPage{}, ErrBudget
				}
				return SearchPage{}, e
			}
			last = next
			for _, stored := range batch {
				if ctx.Err() != nil {
					return SearchPage{}, ErrBudget
				}
				c := searchCandidate{row: stored.RowID, id: stored.Node.ID, parent: stored.Node.ParentID, target: stored.TargetID, path: stored.Path}
				if !scope.accepts(c) {
					continue
				}
				if r.Text != "" && !((r.Field != "path" && searchTextMatches(stored.Name, needle, r.MatchMode)) || (r.Field != "name" && searchTextMatches(stored.Path, needle, r.MatchMode))) {
					continue
				}
				item, e := searchOrderItem(ctx, db, h, r, stored, scope.projection)
				if e != nil {
					return SearchPage{}, e
				}
				if !r.Filter.matches(stored.Node, stored.Name, item.Size, h.FileTypeMapping) {
					continue
				}
				c.key, e = candidateKey(r, item)
				if e != nil {
					return SearchPage{}, e
				}
				if after != "" && !candidateLess(boundary, c, desc) {
					continue
				}
				at := sort.Search(len(best), func(i int) bool { return candidateLess(c, best[i], desc) })
				if at > r.Limit {
					continue
				}
				best = append(best, searchCandidate{})
				copy(best[at+1:], best[at:])
				best[at] = c
				if len(best) > r.Limit+1 {
					best = best[:r.Limit+1]
				}
				retained := 0
				for _, v := range best {
					retained += len(v.id) + len(v.target) + len(v.path) + len(v.key) + 256
				}
				if retained > MaxResponseBytes {
					return SearchPage{}, ErrBudget
				}
			}
			if exhausted {
				break
			}
		}
	}
	out := SearchPage{SnapshotID: r.SnapshotID, RootID: r.RootID, Items: []SearchItem{}, SelectionOrder: r.Sort + "-" + r.Direction + "-recorded-rowid", Metric: r.Metric, Scope: r.Scope, HasMore: len(best) > r.Limit}
	for i := 0; i < len(best) && i < r.Limit; i++ {
		stored, e := reader.Read(best[i].id)
		if e != nil {
			return SearchPage{}, e
		}
		item, e := searchItemRecord(ctx, db, h, r, stored, scope.projection)
		if e != nil {
			return SearchPage{}, e
		}
		out.Items = append(out.Items, item)
	}
	if out.HasMore {
		out.NextCursor = encodeSearchCursor(r, best[r.Limit-1].row)
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > MaxResponseBytes || ctx.Err() != nil {
		return SearchPage{}, ErrBudget
	}
	return out, nil
}

// searchOrderItem decodes full public metadata only when a directory aggregate is needed.
// Physical file sorting/filtering uses the exact recorded decimal values directly.
func searchOrderItem(ctx context.Context, db *sql.DB, h index.GenerationHeader, r SearchRequest, stored index.NodeRecord, projection *engine) (SearchItem, error) {
	n := stored.Node
	item := SearchItem{Name: n.OriginalName, Kind: n.Kind}
	if r.CategorySort && n.Kind == "file" {
		c := category(stored.Name, h.FileTypeMapping)
		item.Classification = &Classification{Category: &c}
	}
	if n.Kind == "file" {
		item.Size = n.LogicalBytes
		if r.Metric == "allocated" {
			item.Size = n.AllocatedBytes
		}
	}
	if n.Kind == "directory" && (r.Sort == "size" || r.Filter.MinSize != nil || r.Filter.MaxSize != nil) {
		return searchItemRecord(ctx, db, h, r, stored, projection)
	}
	return item, nil
}

// searchItem resolves only numeric snapshot locators through the authoritative tree reader.
func searchItem(ctx context.Context, db *sql.DB, h index.GenerationHeader, r SearchRequest, v searchCandidate, projections ...*engine) (SearchItem, error) {
	stored, err := index.ReadNode(ctx, db, v.id)
	if err != nil {
		return SearchItem{}, err
	}
	if stored.RowID != v.row {
		return SearchItem{}, ErrSnapshot
	}
	return searchItemRecord(ctx, db, h, r, stored, projections...)
}

// searchItemRecord preserves virtual parent projection and exact aggregate sizes without repeated path SQL.
func searchItemRecord(ctx context.Context, db *sql.DB, h index.GenerationHeader, r SearchRequest, stored index.NodeRecord, projections ...*engine) (SearchItem, error) {
	e := engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: r.SnapshotID, Metric: r.Metric, Filter: Filter{Categories: []string{}}}, now: time.Now}
	rec, err := e.decodeRecord(stored)
	if err != nil {
		return SearchItem{}, err
	}
	if len(projections) > 0 && projections[0] != nil {
		p := projections[0]
		if adjusted, ok := p.projected[stored.Node.ID]; ok {
			rec = adjusted
		}
		if parent, ok := p.parents[stored.Node.ID]; ok {
			rec.node.ParentID = &parent
		}
	}
	n := stored.Node
	out := SearchItem{Classification: rec.node.Classification, rawName: stored.Name, SnapshotID: n.SnapshotID, NodeID: n.ID, ParentID: rec.node.ParentID, Name: n.OriginalName, Path: n.RelativePath, Kind: n.Kind, VolumeID: n.VolumeID, ShareID: n.ShareID, ModifiedAt: nil, Summary: rec.node.Summary}
	if (n.Kind == "file" || n.Kind == "directory") && out.Summary.UnknownFileCount == "0" {
		size := out.Summary.KnownBytes
		out.Size = &size
	}
	// Zero observed bytes do not prove that an inaccessible or excluded folder is empty.
	if h.ScanBoundaryCount != "0" && n.Kind == "directory" && out.Size != nil && *out.Size == "0" {
		unknown, err := index.DirectorySizeUnmeasured(ctx, db, stored.TargetID, stored.Path)
		if err != nil {
			return SearchItem{}, err
		}
		if unknown {
			out.Size = nil
		}
	}
	return out, nil
}

// searchDisplay matches the canonical public representation of invalid Unix pathname bytes.
func searchDisplay(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw)
	}
	return strconv.QuoteToASCII(string(raw))
}
