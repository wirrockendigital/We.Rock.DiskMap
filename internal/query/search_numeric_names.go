package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// numericNameScope translates already-qualified topology membership to native numeric
// ancestry. Excluded subtree roots are never entered. No filesystem or inferred ID range is used.
func numericNameScope(s searchScope) (string, []any) {
	const columns = "id,parent_id,root_id,name_bytes,kind,logical_bytes,allocated_bytes"
	args := []any{}
	list := func(values []string) string {
		sort.Strings(values)
		marks := make([]string, len(values))
		for i, v := range values {
			marks[i] = "?"
			args = append(args, v)
		}
		if len(marks) == 0 {
			return "NULL"
		}
		return strings.Join(marks, ",")
	}
	keys := func(m map[string]bool) []string {
		v := []string{}
		for k, yes := range m {
			if yes {
				v = append(v, k)
			}
		}
		return v
	}
	where := ""
	switch {
	case s.members != nil:
		where = "id IN (" + list(keys(s.members)) + ")"
	case s.only != "":
		where = "id=?"
		args = append(args, s.only)
	case s.targets != nil:
		where = "root_id IN (SELECT id FROM roots WHERE target_id IN (" + list(keys(s.targets)) + "))"
		if s.direct {
			where += " AND parent_id IS NULL"
		}
	case s.direct:
		where = "parent_id=?"
		args = append(args, s.parent)
	default:
		args = append(args, s.parent)
		excluded := ""
		if len(s.excludedIDs) > 0 {
			excluded = " WHERE c.id NOT IN (" + list(append([]string{}, s.excludedIDs...)) + ")"
		}
		// Carry only row identities through recursive scope expansion, not repeated file metadata.
		query := "WITH RECURSIVE scoped(id) AS (SELECT id FROM nodes WHERE id=? UNION ALL SELECT c.id FROM nodes c JOIN scoped p ON c.parent_id=p.id" + excluded + ") "
		selected := "SELECT n." + strings.ReplaceAll(columns, ",", ",n.") + " FROM scoped s JOIN nodes n ON n.id=s.id"
		if s.includeRoot {
			return query + selected, args
		}
		args = append(args, s.parent)
		return query + selected + " WHERE n.id<>?", args
	}
	if len(s.excludedIDs) > 0 {
		where += " AND id NOT IN (" + list(append([]string{}, s.excludedIDs...)) + ")"
	}
	return "SELECT " + columns + " FROM nodes WHERE " + where, args
}

// searchNumericNames streams raw candidates and retains only the globally sorted top51.
// Public paths and metadata are reconstructed after the cursor is closed, for final results only.
func searchNumericNames(ctx context.Context, db *sql.DB, h index.GenerationHeader, r SearchRequest, s searchScope, boundary searchCandidate, hasBoundary bool, needle string) ([]searchCandidate, error) {
	// Resolve directory ancestry before opening the streaming cursor on the leased DB.
	var paths *searchPathIndex
	var err error
	if r.Field != "name" && r.Text != "" {
		paths, err = searchDirectoryPaths(ctx, db, s)
		if err != nil {
			return nil, err
		}
	}
	source, args := numericNameScope(s)
	aggregate := "NULL"
	join := ""
	needSize := r.Sort == "size" || r.Filter.MinSize != nil || r.Filter.MaxSize != nil
	if needSize {
		aggregate = "a.aggregate_json"
		join = " LEFT JOIN directory_aggregates a ON a.node_id=CASE WHEN n.kind='directory' THEN n.id END"
	}
	rows, err := db.QueryContext(ctx, "SELECT n.id,n.parent_id,n.name_bytes,n.kind,n.logical_bytes,n.allocated_bytes,"+aggregate+" FROM ("+source+") n"+join, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ErrBudget
		}
		return nil, err
	}
	defer rows.Close()
	best := []searchCandidate{}
	retained := 0
	desc := r.Direction == "desc"
	for rows.Next() {
		if ctx.Err() != nil {
			return nil, ErrBudget
		}
		var row int64
		var parent sql.NullInt64
		var name []byte
		var kind string
		var logical, allocated, raw sql.NullString
		if err = rows.Scan(&row, &parent, &name, &kind, &logical, &allocated, &raw); err != nil {
			return nil, err
		}
		matched := r.Field != "path" && searchTextMatches(name, needle, r.MatchMode)
		if !matched && paths != nil {
			path := []byte(".")
			if parent.Valid {
				base, pathErr := paths.path(parent.Int64)
				if pathErr != nil {
					return nil, pathErr
				}
				path = nil
				if string(base) != "." {
					path = append(append([]byte{}, base...), '/')
				}
				path = append(path, name...)
			}
			matched = searchTextMatches(path, needle, r.MatchMode)
		}
		if r.Text != "" && !matched {
			continue
		}
		n := contracts.Node{Kind: kind}
		var size *contracts.Bytes
		value := logical
		if r.Metric == "allocated" {
			value = allocated
		}
		if kind == "file" && value.Valid {
			v := contracts.Bytes(value.String)
			if v.Validate() != nil || len(v) > 128 {
				return nil, ErrSnapshot
			}
			size = &v
		}
		id := strconv.FormatInt(row, 10)
		if kind == "directory" && needSize {
			var sum Summary
			if projected, ok := s.projection.projected[id]; ok {
				sum = projected.node.Summary
			} else {
				var a contracts.DirectoryAggregate
				if !raw.Valid || len(raw.String) > 32768 || json.Unmarshal([]byte(raw.String), &a) != nil {
					return nil, ErrSnapshot
				}
				sum, err = content(a.Subtree, Request{Metric: r.Metric})
				if err != nil {
					return nil, err
				}
			}
			if sum.UnknownFileCount == "0" {
				v := sum.KnownBytes
				size = &v
			}
		}
		if r.Filter.Text != "" || r.Sort == "name" || r.CategorySort {
			n.OriginalName = searchDisplay(name)
		}
		if !r.Filter.matches(n, name, size, h.FileTypeMapping) {
			continue
		}
		item := SearchItem{Name: n.OriginalName, Kind: kind, Size: size}
		if r.CategorySort && kind == "file" {
			c := category(name, h.FileTypeMapping)
			item.Classification = &Classification{Category: &c}
		}
		key, e := candidateKey(r, item)
		if e != nil {
			return nil, e
		}
		c := searchCandidate{row: row, id: id, key: key}
		if hasBoundary && !candidateLess(boundary, c, desc) {
			continue
		}
		at := sort.Search(len(best), func(i int) bool { return candidateLess(c, best[i], desc) })
		if at > r.Limit {
			continue
		}
		best = append(best, searchCandidate{})
		copy(best[at+1:], best[at:])
		best[at] = c
		retained += candidateBytes(c)
		if len(best) > r.Limit+1 {
			retained -= candidateBytes(best[len(best)-1])
			best = best[:r.Limit+1]
		}
		if retained > MaxResponseBytes {
			return nil, ErrBudget
		}
	}
	if ctx.Err() != nil {
		return nil, ErrBudget
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return best, nil
}
