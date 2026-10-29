package query

import (
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// SourceProvenance exposes historical authority without private paths or descriptor bindings.
type SourceProvenance struct {
	Version       int     `json:"version"`
	Authority     string  `json:"authority"`
	SourceID      *string `json:"sourceId"`
	Qualification string  `json:"qualification"`
	State         string  `json:"state"`
}

// ValidSourceProvenance validates only the fixed public authority combinations.
func ValidSourceProvenance(p *SourceProvenance) bool {
	if p == nil || p.Version != 1 || (p.SourceID != nil && !security.ValidID(*p.SourceID)) {
		return false
	}
	switch p.State {
	case "reachable", "missing", "unreadable", "locked", "unmounted", "unverified", "unavailable":
	default:
		return false
	}
	switch p.Authority {
	case "native-inventory":
		return p.Qualification == "unavailable" || p.Qualification == "qualified" && p.State == "reachable" && p.SourceID != nil
	case "explicit-roots", "synthetic-fixture":
		return p.Qualification == "explicit"
	}
	return false
}

// real applies the same historical parent projection used by hierarchy and navigation.
func (e *engine) real(id string) (record, error) {
	if err := e.topology(); err != nil {
		return record{}, err
	}
	if v, ok := e.projected[id]; ok {
		return v, nil
	}
	v, err := e.rawReal(id)
	if err != nil {
		return v, err
	}
	if parent, ok := e.parents[id]; ok {
		v.node.ParentID = &parent
	}
	return v, nil
}

// topology materializes only bounded source boundaries, never the complete indexed hierarchy.
func (e *engine) topology() error {
	if e.sourceReady {
		return nil
	}
	if err := index.ValidateSourceProvenance(e.h.Manifest); err != nil {
		return ErrSnapshot
	}
	if err := e.baseTopology(); err != nil {
		return err
	}
	e.projected = map[string]record{}
	e.replacements = map[string]record{}
	e.excluded = map[string]map[string]bool{}
	e.containers = map[string]string{}
	e.parents = map[string]string{}
	for _, root := range e.h.Manifest.Provenance.Roots {
		volumeID := virtualID(e.h.SnapshotID, "volume", root.VolumeID)
		vol, exists := e.roots[volumeID]
		if !exists {
			continue
		}
		if root.VolumeName != "" {
			vol.node.Name = root.VolumeName
			e.roots[volumeID] = vol
		}
		rootNode, err := index.FindNode(e.ctx, e.db, root.TargetID, []byte("."))
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		rootID := rootNode.Node.ID
		real, err := e.rawReal(rootID)
		if errors.Is(err, ErrRoot) {
			continue
		}
		if err != nil {
			return err
		}
		e.parents[rootID] = volumeID
		if e.h.Manifest.Provenance.Authority != "native-inventory" {
			// Declared roots provide measured content but no native share-catalog authority.
			wrapper := e.sourceWrapper(real, root.TargetID, root.Name, "other", volumeID, root.SourceID, "explicit", "reachable")
			e.replaceVolumeChild(volumeID, rootID, []record{wrapper})
			continue
		}
		if root.Kind == "share" {
			wrapper := e.sourceWrapper(real, root.TargetID, root.Name, "share", volumeID, root.SourceID, "qualified", "reachable")
			e.replaceVolumeChild(volumeID, rootID, []record{wrapper})
			continue
		}
		remaining := real
		directShares := []record{}
		for _, area := range root.Areas {
			if area.Qualification != "qualified" || area.Binding == nil || area.SourceID == nil {
				continue
			}
			areaNode, err := index.FindNode(e.ctx, e.db, root.TargetID, area.RelativePath)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			id := areaNode.Node.ID
			child, err := e.rawReal(id)
			if errors.Is(err, ErrRoot) {
				continue
			}
			if err != nil {
				return err
			}
			if child.node.Kind != "directory" || child.node.ParentID == nil {
				return ErrSnapshot
			}
			parent := *child.node.ParentID
			wrapper := e.sourceWrapper(child, *area.SourceID, area.Name, "share", parent, area.SourceID, area.Qualification, area.State)
			if parent == rootID {
				wrapper.node.ParentID = &volumeID
				e.roots[wrapper.node.ID] = wrapper
				directShares = append(directShares, wrapper)
				if e.excluded[rootID] == nil {
					e.excluded[rootID] = map[string]bool{}
				}
				e.excluded[rootID][id] = true
				if !e.r.Filter.HasPredicates() || e.r.Mode != "hits-only" {
					remaining.node.Summary, err = merge(remaining.node.Summary, child.node.Summary, true)
				}
				if err != nil {
					return err
				}
				remaining.node.OriginalSummary, err = merge(remaining.node.OriginalSummary, child.node.OriginalSummary, true)
				if err != nil {
					return err
				}
				if !e.r.Filter.HasPredicates() {
					remaining.node.MatchSummary, err = merge(remaining.node.MatchSummary, child.node.MatchSummary, true)
				}
				if err != nil {
					return err
				}
				if count(child.node.Summary) != "0" && (!e.r.Filter.HasPredicates() || e.r.Mode != "hits-only") {
					remaining.direct.DirectoryCount, err = arithmetic(remaining.direct.DirectoryCount, "1", true)
					if err != nil {
						return err
					}
				}
			} else {
				e.replacements[id] = wrapper
			}
		}
		remaining.node.HasMore = count(remaining.direct) != "0"
		e.projected[rootID] = remaining
		other := e.sourceWrapper(remaining, root.TargetID, "Other recorded areas", "other", volumeID, root.SourceID, "qualified", "reachable")
		e.projected[rootID] = e.virtual[other.node.ID][0]
		e.replaceVolumeChild(volumeID, rootID, append(directShares, other))
	}
	// Reaggregate the disjoint partition before pruning zero-hit presentation edges.
	nasID := virtualID(e.h.SnapshotID, "nas", "")
	nas := e.roots[nasID]
	nas.node.Summary, nas.node.OriginalSummary, nas.node.MatchSummary = zero(), zero(), zero()
	visibleVolumes := []record{}
	for _, old := range e.virtual[nasID] {
		volume := e.roots[old.node.ID]
		volume.node.Summary, volume.node.OriginalSummary, volume.node.MatchSummary = zero(), zero(), zero()
		visibleChildren := []record{}
		for _, child := range e.virtual[volume.node.ID] {
			var err error
			volume.node.Summary, err = merge(volume.node.Summary, child.node.Summary, false)
			if err != nil {
				return err
			}
			volume.node.OriginalSummary, err = merge(volume.node.OriginalSummary, child.node.OriginalSummary, false)
			if err != nil {
				return err
			}
			volume.node.MatchSummary, err = merge(volume.node.MatchSummary, child.node.MatchSummary, false)
			if err != nil {
				return err
			}
			if e.r.Mode != "hits-only" || count(child.node.Summary) != "0" {
				visibleChildren = append(visibleChildren, child)
			}
		}
		volume.node.HasMore = len(visibleChildren) > 0
		e.virtual[volume.node.ID] = visibleChildren
		e.roots[volume.node.ID] = volume
		var err error
		nas.node.Summary, err = merge(nas.node.Summary, volume.node.Summary, false)
		if err != nil {
			return err
		}
		nas.node.OriginalSummary, err = merge(nas.node.OriginalSummary, volume.node.OriginalSummary, false)
		if err != nil {
			return err
		}
		nas.node.MatchSummary, err = merge(nas.node.MatchSummary, volume.node.MatchSummary, false)
		if err != nil {
			return err
		}
		if e.r.Mode != "hits-only" || count(volume.node.Summary) != "0" {
			visibleVolumes = append(visibleVolumes, volume)
		}
	}
	nas.node.HasMore = len(visibleVolumes) > 0
	e.virtual[nasID] = visibleVolumes
	e.roots[nasID] = nas
	e.sourceReady = true
	return nil
}

// sourceWrapper adds no physical object or count; its only child owns the canonical directory entry.
func (e *engine) sourceWrapper(real record, key, name, kind, parent string, source *string, qualification, state string) record {
	id := virtualID(e.h.SnapshotID, kind, key)
	wrapper := real
	wrapper.node = real.node
	wrapper.node.ID = id
	wrapper.node.ParentID = &parent
	wrapper.node.Name = name
	wrapper.node.Kind = kind
	wrapper.node.Virtual = true
	wrapper.node.RealNodeID = nil
	// A virtual source container has no filesystem timestamp of its own.
	wrapper.node.ModifiedAt = nil
	wrapper.node.Classification = nil
	wrapper.node.HasMore = true
	wrapper.node.Provenance = &SourceProvenance{Version: 1, Authority: e.h.Manifest.Provenance.Authority, SourceID: source, Qualification: qualification, State: state}
	real.node.ParentID = &id
	e.parents[real.node.ID] = id
	e.virtual[id] = []record{real}
	e.roots[id] = wrapper
	e.containers[id] = real.node.ID
	return wrapper
}

// replaceVolumeChild preserves root order and only removes the exact canonical source root.
func (e *engine) replaceVolumeChild(volume, id string, children []record) {
	out := []record{}
	for _, v := range e.virtual[volume] {
		if v.node.ID == id {
			for _, child := range children {
				out = append(out, child)
			}
		} else {
			out = append(out, v)
		}
	}
	e.virtual[volume] = out
}
