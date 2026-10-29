package query

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"time"
)

type searchScope struct {
	members     map[string]bool
	projection  *engine
	only        string
	includeRoot bool
	excluded    [][]byte
	excludedIDs []string
	parent      string
	target      string
	prefix      []byte
	targets     map[string]bool
	direct      bool
}

// resolveSearchScope obtains identity only from the leased generation, never a source filesystem path.
func resolveSearchScope(ctx context.Context, db *sql.DB, h index.GenerationHeader, r SearchRequest) (searchScope, error) {
	s := searchScope{direct: r.Scope == "direct"}
	engine := &engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: h.SnapshotID, Metric: r.Metric, Filter: Filter{Categories: []string{}}}, now: time.Now}
	s.projection = engine
	if err := engine.topology(); err != nil {
		return s, err
	}
	if s.direct {
		virtual := r.RootID
		if virtual == "nas" {
			virtual = virtualID(h.SnapshotID, "nas", "")
		}
		if _, ok := engine.virtual[virtual]; ok {
			s.members = map[string]bool{}
			var visit func(string) error
			visit = func(id string) error {
				if children, ok := engine.virtual[id]; ok {
					for _, child := range children {
						if err := visit(child.node.ID); err != nil {
							return err
						}
					}
					return nil
				}
				if s.members[id] {
					return nil
				}
				s.members[id] = true
				if _, err := engine.real(id); err != nil {
					return err
				}
				return nil
			}
			if err := visit(virtual); err != nil {
				return s, err
			}
			return s, nil
		}
	}
	realID := r.RootID
	if id, ok := engine.containers[r.RootID]; ok {
		realID = id
		s.includeRoot = true
		if s.direct {
			s.only = id
		}
	}
	if realID != r.RootID || len(engine.excluded[realID]) > 0 {
		stored, err := index.ReadNode(ctx, db, realID)
		if err != nil || stored.Node.Kind != "directory" {
			return s, ErrRoot
		}
		s.target, s.prefix = stored.TargetID, stored.Path
		s.parent = realID
		for id := range engine.excluded[realID] {
			var raw []byte
			stored, err := index.ReadNode(ctx, db, id)
			if err != nil {
				return s, err
			}
			raw = stored.Path
			s.excluded = append(s.excluded, raw)
			s.excludedIDs = append(s.excludedIDs, id)
		}
		return s, nil
	}

	if r.RootID == "nas" || r.RootID == virtualID(r.SnapshotID, "nas", "") {
		s.targets = map[string]bool{}
		for _, root := range h.Manifest.Roots {
			s.targets[root.TargetID] = true
		}
		return s, nil
	}
	for _, root := range h.Manifest.Roots {
		if r.RootID == virtualID(r.SnapshotID, "volume", root.VolumeID) {
			s.targets = map[string]bool{}
			for _, other := range h.Manifest.Roots {
				if other.VolumeID == root.VolumeID {
					s.targets[other.TargetID] = true
				}
			}
			return s, nil
		}
	}
	stored, err := index.ReadNode(ctx, db, r.RootID)
	if err != nil || stored.Node.Kind != "directory" {
		return s, ErrRoot
	}
	s.target, s.prefix = stored.TargetID, stored.Path
	s.parent = r.RootID
	return s, nil
}

// accepts checks byte-exact ancestry; common display prefixes never manufacture scope membership.
func (s searchScope) accepts(c searchCandidate) bool {
	if s.members != nil {
		return s.members[c.id]
	}
	for _, excluded := range s.excluded {
		if bytes.Equal(c.path, excluded) || bytes.HasPrefix(c.path, append(append([]byte{}, excluded...), '/')) {
			return false
		}
	}
	if s.only != "" {
		return c.id == s.only
	}

	if s.targets != nil {
		return s.targets[c.target] && (!s.direct || c.parent == nil)
	}
	if s.direct {
		return c.parent != nil && *c.parent == s.parent
	}
	if c.target != s.target || bytes.Equal(c.path, s.prefix) && !s.includeRoot {
		return false
	}
	return s.includeRoot && bytes.Equal(c.path, s.prefix) || bytes.Equal(s.prefix, []byte(".")) || bytes.HasPrefix(c.path, append(append([]byte{}, s.prefix...), '/'))
}
