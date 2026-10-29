package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Hierarchy returns a conserved bounded projection of a caller-leased sealed SQLite generation.
func Hierarchy(ctx context.Context, db *sql.DB, h index.GenerationHeader, request Request) (Response, error) {
	return hierarchy(ctx, db, h, request, time.Now)
}

// hierarchy accepts an internal clock only so soft-budget transitions can be tested deterministically.
func hierarchy(ctx context.Context, db *sql.DB, h index.GenerationHeader, request Request, now func() time.Time) (Response, error) {
	return hierarchyProjection(ctx, db, nil, h, request, now)
}

// hierarchyProjection shares all expansion budgets with filtered reads.
func hierarchyProjection(ctx context.Context, db, projection *sql.DB, h index.GenerationHeader, request Request, now func() time.Time) (Response, error) {
	expandUntil := now().Add(ExpansionBudget)
	r, err := request.Validate()
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if err = checkHeader(ctx, db, h, r); err != nil {
		return Response{}, err
	}
	if err = qualifyFilterProjection(ctx, projection, h, r); err != nil {
		return Response{}, err
	}
	e := engine{ctx: ctx, db: db, projection: projection, h: h, r: r, now: now, expandUntil: expandUntil}
	root, err := e.root()
	if err != nil {
		return Response{}, err
	}
	root.node.ParentID = nil
	out := Response{SnapshotID: r.SnapshotID, RootID: root.node.ID, Metric: r.Metric, Filter: r.Filter, Depth: r.Depth, ElementBudget: r.ElementBudget, SelectionOrder: "recorded-rowid", Nodes: []Node{root.node}, State: h.State, Coverage: h.Coverage}
	out.SourceAvailability, err = e.sourceAvailability(root.node.ID)
	if err != nil {
		return Response{}, err
	}
	out.Mode = r.Mode
	out.OriginalSummary = root.node.OriginalSummary
	out.MatchSummary = root.node.MatchSummary
	out.Denominator = root.node.Summary
	out.MatchState = filterMatchState(root.node)
	initial, _ := json.Marshal(out)
	used := len(initial)
	if used > MaxResponseBytes-4096 {
		return Response{}, ErrBudget
	}
	type pending struct {
		v               record
		position, depth int
	}
	queue := []pending{{root, 0, 0}}
	// Every queued expandable parent may still need its own exact residual group.
	// Reserving globally prevents earlier branches from consuming later parents' space.
	const groupReserve = 4096
	reserved := 0
	if root.node.HasMore {
		reserved = groupReserve
	}
	for head := 0; head < len(queue); head++ {
		if err = ctx.Err(); err != nil {
			return Response{}, err
		}
		p := queue[head]
		if !p.v.node.HasMore {
			continue
		}
		reserved -= groupReserve
		remaining := r.ElementBudget - len(out.Nodes)
		if remaining == 0 {
			out.Truncated = true
			continue
		}
		total := p.v.node.Summary
		originalTotal := p.v.node.OriginalSummary
		matchTotal := p.v.node.MatchSummary
		var childCount contracts.Bytes
		if children, virtual := e.virtual[p.v.node.ID]; virtual {
			childCount = contracts.Bytes(fmt.Sprint(len(children)))
		} else {
			total.DirectoryCount, err = arithmetic(total.DirectoryCount, "1", true)
			if err != nil {
				return Response{}, err
			}
			childCount = count(p.v.direct)
			if originalTotal.DirectoryCount != "0" {
				originalTotal.DirectoryCount, _ = arithmetic(originalTotal.DirectoryCount, "1", true)
			}
			if matchTotal.DirectoryCount != "0" {
				matchTotal.DirectoryCount, _ = arithmetic(matchTotal.DirectoryCount, "1", true)
			}
		}
		reason := "element-budget"
		limit := remaining - 1
		if p.depth >= r.Depth {
			limit = 0
			reason = "depth-limit"
		}
		// Physical rows, including filtered-out rows, are charged to the request-wide work budget.
		work := 2*r.ElementBudget - e.rows
		if limit > work {
			limit = work
		}
		if limit < 0 {
			limit = 0
		}
		if e.expansionExpired() {
			limit = 0
			reason = "time-budget"
		}
		var after int64
		accepted := 0
		readAfter := int64(0)
		initialPage := true
		// Commit each bounded decoded batch before consulting the optional clock again.
		// This preserves useful work when I/O consumes the remaining expansion time.
		for accepted < limit {
			if e.expansionExpired() {
				reason = "time-budget"
				break
			}
			batchLimit := min(128, limit-accepted, 2*r.ElementBudget-e.rows)
			if batchLimit <= 0 {
				break
			}
			selected, more, last, readErr := e.children(p.v.node.ID, readAfter, batchLimit, initialPage)
			if readErr != nil {
				return Response{}, readErr
			}
			before := accepted
			for _, v := range selected {
				// The hard caller deadline still applies while a decoded batch is adopted.
				if err = ctx.Err(); err != nil {
					return Response{}, err
				}
				v.node.ParentID = &p.v.node.ID
				raw, _ := json.Marshal(v.node)
				childReserve := 0
				if v.node.HasMore {
					childReserve = groupReserve
				}
				// Keep space for this parent and all previously queued expandable children.
				if used+len(raw)+1+reserved+groupReserve+childReserve > MaxResponseBytes {
					reason = "byte-budget"
					break
				}
				next, er := merge(total, v.node.Summary, true)
				if er != nil {
					return Response{}, er
				}
				total = next
				originalTotal, er = merge(originalTotal, v.node.OriginalSummary, true)
				if er != nil {
					return Response{}, er
				}
				matchTotal, er = merge(matchTotal, v.node.MatchSummary, true)
				if er != nil {
					return Response{}, er
				}
				childCount, er = arithmetic(childCount, "1", true)
				if er != nil {
					return Response{}, er
				}
				used += len(raw) + 1
				reserved += childReserve
				after = v.row
				accepted++
				out.Nodes = append(out.Nodes, v.node)
				queue = append(queue, pending{v, len(out.Nodes) - 1, p.depth + 1})
			}
			if accepted-before != len(selected) {
				break
			}
			if e.bufferLimited {
				reason = "byte-budget"
				break
			}
			if e.timeLimited {
				reason = "time-budget"
				break
			}
			if !more {
				break
			}
			// Resume after consumed physical rows, including filtered-out children.
			// The lookahead row is deliberately absent from this continuation key.
			readAfter, initialPage = last, false
		}
		if childCount != "0" {
			id := virtualID(r.SnapshotID, "group", p.v.node.ID)
			cr := r
			cr.RootID = p.v.node.ID
			// Virtual topology uses ordinal keys; real children use SQLite's stable recorded rowid.
			if _, virtual := e.virtual[p.v.node.ID]; virtual {
				after = int64(accepted)
			}
			c := ""
			if accepted > 0 {
				c = cursor(cr, after)
			}
			g := Node{ID: id, ParentID: &p.v.node.ID, Name: "", Kind: "group", Virtual: true, Summary: total, OriginalSummary: originalTotal, MatchSummary: matchTotal, MatchKind: "group", HasMore: true, GroupReason: reason, GroupedChildren: childCount, ListRootID: p.v.node.ID, AfterCursor: c}
			raw, _ := json.Marshal(g)
			if used+len(raw)+1 > MaxResponseBytes {
				return Response{}, ErrBudget
			}
			used += len(raw) + 1
			out.Nodes = append(out.Nodes, g)
			out.Truncated = true
		} else {
			out.Nodes[p.position].HasMore = false
		}
	}
	encoded, err := json.Marshal(out)
	if deadlineErr := ctx.Err(); deadlineErr != nil {
		return Response{}, deadlineErr
	}
	if err != nil || len(encoded) > MaxResponseBytes {
		return Response{}, ErrBudget
	}
	return out, nil
}

// Children provides bounded direct-child continuation for group lists without re-walking preceding rows.
func Children(ctx context.Context, db *sql.DB, h index.GenerationHeader, request Request, afterCursor string) (Page, error) {
	return childrenProjection(ctx, db, nil, h, request, afterCursor)
}

// childrenProjection shares cursor and work bounds with the ordinary child reader.
func childrenProjection(ctx context.Context, db, projection *sql.DB, h index.GenerationHeader, request Request, afterCursor string) (Page, error) {
	expandUntil := time.Now().Add(ExpansionBudget)
	r, err := request.Validate()
	if err != nil {
		return Page{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	if err = checkHeader(ctx, db, h, r); err != nil {
		return Page{}, err
	}
	if err = qualifyFilterProjection(ctx, projection, h, r); err != nil {
		return Page{}, err
	}
	e := engine{ctx: ctx, db: db, projection: projection, h: h, r: r, now: time.Now, expandUntil: expandUntil}
	root, err := e.root()
	if err != nil {
		return Page{}, err
	}
	r.RootID = root.node.ID
	e.r = r
	after, err := parseCursor(r, afterCursor)
	if err != nil {
		return Page{}, err
	}
	out := Page{SnapshotID: r.SnapshotID, RootID: r.RootID, Metric: r.Metric, Filter: r.Filter, Nodes: []Node{}, SelectionOrder: "recorded-rowid"}
	out.Mode = r.Mode
	out.OriginalSummary = root.node.OriginalSummary
	out.MatchSummary = root.node.MatchSummary
	out.Denominator = root.node.Summary
	out.MatchState = filterMatchState(root.node)
	items, more, last, err := e.children(root.node.ID, after, r.ElementBudget, afterCursor == "")
	if err != nil {
		return Page{}, err
	}
	size := 1024
	accepted := after
	for i, v := range items {
		v.node.ParentID = &root.node.ID
		b, _ := json.Marshal(v.node)
		if size+len(b) > MaxResponseBytes {
			more = true
			last = accepted
			break
		}
		size += len(b) + 1
		out.Nodes = append(out.Nodes, v.node)
		accepted = v.row
		if _, virtual := e.virtual[root.node.ID]; virtual {
			accepted = after + int64(i) + 1
		}
	}
	out.HasMore = more
	if more {
		out.NextCursor = cursor(r, last)
	}
	encoded, _ := json.Marshal(out)
	if deadlineErr := ctx.Err(); deadlineErr != nil {
		return Page{}, deadlineErr
	}
	if len(encoded) > MaxResponseBytes {
		return Page{}, ErrBudget
	}
	return out, nil
}
