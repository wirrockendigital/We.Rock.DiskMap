package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// NavigationPart contains only names and identities verified in the leased generation.
type NavigationPart struct {
	Provenance *SourceProvenance `json:"provenance,omitempty"`
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
}

// Navigation is independent of filters: hidden ancestors retain their real identity.
type Navigation struct {
	SnapshotID     string           `json:"snapshotId"`
	RootID         string           `json:"rootId"`
	ParentID       *string          `json:"parentId"`
	Breadcrumb     []NavigationPart `json:"breadcrumb"`
	SelectedNodeID *string          `json:"selectedNodeId"`
	Reasons        []string         `json:"reasons"`
}

// ReadNavigation resolves real parent edges and the same virtual topology used by hierarchy queries.
// Missing opaque identities cannot prove an ancestor; they fall back explicitly to the qualified NAS root.
func ReadNavigation(ctx context.Context, db *sql.DB, h index.GenerationHeader, root string, selected *string) (Navigation, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	out := Navigation{SnapshotID: h.SnapshotID, Breadcrumb: []NavigationPart{}, Reasons: []string{}}
	e := engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Filter: Filter{Categories: []string{}}}, now: time.Now}
	if err := checkHeader(ctx, db, h, e.r); err != nil {
		return out, err
	}
	if err := e.topology(); err != nil {
		return out, err
	}
	nas := virtualID(h.SnapshotID, "nas", "")
	lookup := func(id string) (Node, error) {
		if id == "nas" {
			id = nas
		}
		if r, ok := e.roots[id]; ok {
			return r.node, nil
		}
		r, err := e.real(id)
		if err != nil {
			return Node{}, err
		}
		if r.node.ParentID == nil {
			p := virtualID(h.SnapshotID, "volume", r.node.VolumeID)
			if _, ok := e.roots[p]; !ok {
				return Node{}, ErrSnapshot
			}
			r.node.ParentID = &p
		}
		return r.node, nil
	}
	chain := func(id string) ([]NavigationPart, error) {
		parts := []NavigationPart{}
		seen := map[string]bool{}
		for len(parts) < 256 {
			n, err := lookup(id)
			if err != nil {
				return nil, err
			}
			if seen[n.ID] {
				return nil, ErrSnapshot
			}
			seen[n.ID] = true
			parts = append(parts, NavigationPart{ID: n.ID, Name: n.Name, Kind: n.Kind, Provenance: n.Provenance})
			if n.ParentID == nil {
				if n.ID != nas {
					return nil, ErrSnapshot
				}
				return parts, nil
			}
			id = *n.ParentID
		}
		return nil, ErrBudget
	}
	n, err := lookup(root)
	if errors.Is(err, ErrRoot) {
		n = e.roots[nas].node
		out.Reasons = append(out.Reasons, "ROOT_UNAVAILABLE")
	} else if err != nil {
		return out, err
	}
	// A file selection is distinct from a navigable directory. Its actual parent is the nearest valid root.
	if n.Kind != "directory" && n.Kind != "volume" && n.Kind != "nas" && n.Kind != "share" && n.Kind != "other" {
		if n.ParentID == nil {
			return out, ErrRoot
		}
		n, err = lookup(*n.ParentID)
		if err != nil {
			return out, err
		}
		out.Reasons = append(out.Reasons, "ROOT_ANCESTOR")
	}
	out.RootID = n.ID
	out.ParentID = n.ParentID
	parts, err := chain(n.ID)
	if err != nil {
		return out, err
	}
	for i := len(parts) - 1; i >= 0; i-- {
		out.Breadcrumb = append(out.Breadcrumb, parts[i])
	}
	if selected != nil {
		ancestors, err := chain(*selected)
		if errors.Is(err, ErrRoot) {
			out.Reasons = append(out.Reasons, "SELECTION_UNAVAILABLE")
		} else if err != nil {
			return out, err
		} else {
			for _, a := range ancestors {
				if a.ID == out.RootID {
					id := ancestors[0].ID
					out.SelectedNodeID = &id
					break
				}
			}
			if out.SelectedNodeID == nil {
				out.Reasons = append(out.Reasons, "SELECTION_OUTSIDE_ROOT")
			}
		}
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 16384 {
		return Navigation{}, ErrBudget
	}
	return out, nil
}
