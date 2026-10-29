package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// VirtualSelection is an opaque, server-qualified identity; it is never accepted from a client.
type VirtualSelection struct{ key, binding string }

// virtualSelectionEngine uses the unfiltered, sealed topology and its existing source-boundary limits.
func virtualSelectionEngine(ctx context.Context, db *sql.DB, h index.GenerationHeader) (*engine, error) {
	e := &engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Filter: Filter{Categories: []string{}}}, now: time.Now}
	if err := checkHeader(ctx, db, h, e.r); err != nil {
		return nil, err
	}
	if err := e.topology(); err != nil {
		return nil, err
	}
	return e, nil
}

// virtualSelectionIdentity excludes labels and snapshot IDs, but retains exact captured source bindings.
func (e *engine) virtualSelectionIdentity(n Node) (VirtualSelection, error) {
	if n.Kind == "nas" {
		return VirtualSelection{key: "nas", binding: e.h.Profile.ID}, nil
	}
	roots := []string{e.h.Manifest.Provenance.Authority}
	for _, r := range e.h.Manifest.Roots {
		if r.VolumeID != n.VolumeID {
			continue
		}
		if r.Binding == nil {
			return VirtualSelection{}, ErrRoot
		}
		b := map[string]any{"target": r.TargetID, "volume": r.VolumeID, "kind": r.Kind, "share": r.ShareID, "binding": r.Binding}
		areas := []any{}
		subvolumes := []any{}
		// Source-area identity changes invalidate wrappers rather than silently transferring authority.
		for _, p := range e.h.Manifest.Provenance.Roots {
			if p.TargetID == r.TargetID {
				b["sourceId"] = p.SourceID
				b["sourceBinding"] = p.Binding
				b["sourceKind"] = p.Kind
				for _, a := range p.Areas {
					a.Name = ""
					areas = append(areas, a)
				}
			}
		}
		for _, s := range e.h.Manifest.QualifiedSubvolumes {
			if s.TargetID == r.TargetID {
				subvolumes = append(subvolumes, s)
			}
		}
		b["areas"] = areas
		b["subvolumes"] = subvolumes
		raw, err := json.Marshal(b)
		if err != nil {
			return VirtualSelection{}, err
		}
		roots = append(roots, string(raw))
	}
	if len(roots) == 1 {
		return VirtualSelection{}, ErrRoot
	}
	sort.Strings(roots)
	key := []any{n.Kind, n.VolumeID}
	if n.Kind != "volume" {
		realID, ok := e.containers[n.ID]
		if !ok {
			return VirtualSelection{}, ErrRoot
		}
		var target string
		var rawPath []byte
		stored, err := index.ReadNode(e.ctx, e.db, realID)
		if err != nil {
			return VirtualSelection{}, err
		}
		target, rawPath = stored.TargetID, stored.Path
		key = append(key, target, rawPath)
		if n.Provenance == nil {
			return VirtualSelection{}, ErrRoot
		}
		key = append(key, n.Provenance.Authority, n.Provenance.Qualification)
		if n.Provenance.SourceID != nil {
			key = append(key, *n.Provenance.SourceID)
		}
	}
	k, _ := json.Marshal(key)
	binding, _ := json.Marshal(roots)
	digest := sha256.Sum256(binding)
	return VirtualSelection{key: string(k), binding: e.h.Profile.ID + string(digest[:])}, nil
}

// QualifyVirtualSelection returns nil for nonvirtual IDs; callers must still qualify their real-node path.
func QualifyVirtualSelection(ctx context.Context, db *sql.DB, h index.GenerationHeader, id string) (*VirtualSelection, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	e, err := virtualSelectionEngine(ctx, db, h)
	if err != nil {
		return nil, err
	}
	v, ok := e.roots[id]
	if !ok || !v.node.Virtual {
		return nil, nil
	}
	s, err := e.virtualSelectionIdentity(v.node)
	if err == ErrRoot {
		return &VirtualSelection{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ResolveVirtualSelection returns an empty identity for a changed or absent source, never a guessed path.
func ResolveVirtualSelection(ctx context.Context, db *sql.DB, h index.GenerationHeader, selection *VirtualSelection) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	e, err := virtualSelectionEngine(ctx, db, h)
	if err != nil {
		return "", err
	}
	if selection != nil && selection.binding == "" {
		return "", nil
	}
	if selection == nil {
		return "", ErrRoot
	}
	for id, v := range e.roots {
		if !v.node.Virtual {
			continue
		}
		candidate, err := e.virtualSelectionIdentity(v.node)
		if err == ErrRoot {
			continue
		}
		if err != nil {
			return "", err
		}
		if candidate == *selection {
			return id, nil
		}
	}
	return "", nil
}
