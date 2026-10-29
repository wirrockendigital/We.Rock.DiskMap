package query

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"time"
)

// ValidateFilterProjection performs a bounded identity/complete check on a caller-owned cache.
func ValidateFilterProjection(ctx context.Context, db *sql.DB, h index.GenerationHeader, metric string, f Filter) error {
	if db == nil {
		return ErrFilterPending
	}
	key, err := FilterProjectionKey(h, metric, f)
	if err != nil {
		return err
	}
	s, err := readFilterState(ctx, db)
	if err != nil || s.key != key {
		return ErrFilterProjection
	}
	if s.phase != "complete" {
		return ErrFilterPending
	}
	return nil
}
func qualifyFilterProjection(ctx context.Context, db *sql.DB, h index.GenerationHeader, r Request) error {
	if !r.Filter.HasPredicates() {
		return nil
	}
	return ValidateFilterProjection(ctx, db, h, r.Metric, r.Filter)
}

// HierarchyFiltered requires a fully qualified cache for active predicates; no partial totals escape.
func HierarchyFiltered(ctx context.Context, source, cache *sql.DB, h index.GenerationHeader, r Request) (Response, error) {
	return hierarchyProjection(ctx, source, cache, h, r, time.Now)
}

// ChildrenFiltered retains the same bounded continuation contract and immutable filter identity.
func ChildrenFiltered(ctx context.Context, source, cache *sql.DB, h index.GenerationHeader, r Request, cursor string) (Page, error) {
	return childrenProjection(ctx, source, cache, h, r, cursor)
}

// applyFilter preserves original weight for highlighting and uses the exact union for hit-only display.
func (e *engine) applyFilter(v record) (record, error) {
	v.node.OriginalSummary = v.node.Summary
	v.node.MatchSummary = v.node.Summary
	v.node.MatchKind = "direct"
	if !e.r.Filter.HasPredicates() {
		return v, nil
	}
	if e.projection == nil {
		return v, ErrFilterPending
	}
	var raw, direct string
	var hit, covered bool
	if err := e.projection.QueryRowContext(e.ctx, `SELECT matched,direct,hit,covered FROM filter_nodes WHERE id=?`, v.node.ID).Scan(&raw, &direct, &hit, &covered); err != nil {
		return v, ErrFilterProjection
	}
	m, err := parseSummary(raw)
	if err != nil {
		return v, err
	}
	v.node.MatchSummary = m
	v.node.MatchKind = "none"
	if hit {
		v.node.MatchKind = "direct"
	} else if covered {
		v.node.MatchKind = "subtree"
	} else if count(m) != "0" {
		v.node.MatchKind = "ancestor"
	}
	if e.r.Mode == "hits-only" {
		v.node.Summary = m
		v.direct, err = parseSummary(direct)
		if err != nil {
			return v, err
		}
		v.node.HasMore = count(v.direct) != "0"
	}
	return v, nil
}

// filterMatchState distinguishes a truly empty directory from a nonempty view with no matches.
func filterMatchState(n Node) string {
	if n.Kind == "directory" && n.OriginalSummary.FileCount == "0" && n.OriginalSummary.DirectoryCount == "1" && n.OriginalSummary.SpecialCount == "0" {
		return "empty"
	}
	if count(n.MatchSummary) == "0" {
		return "no-match"
	}
	return "matches"
}
