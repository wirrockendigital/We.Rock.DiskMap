package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"path"
	"reflect"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// selectionSource contains raw index bytes, not browser display paths or current filesystem guesses.
type selectionSource struct {
	Target   string
	Path     []byte
	Manifest scopemanifest.ScopeManifest
}

// sameSelectionScope rejects changed root identity and changed containing subvolume bindings.
func sameSelectionScope(a, b scopemanifest.ScopeManifest, target, relative string) bool {
	var x, y *scopemanifest.ManifestRoot
	for i := range a.Roots {
		if a.Roots[i].TargetID == target {
			x = &a.Roots[i]
		}
	}
	for i := range b.Roots {
		if b.Roots[i].TargetID == target {
			y = &b.Roots[i]
		}
	}
	if x == nil || y == nil || x.Binding == nil || y.Binding == nil || x.VolumeID != y.VolumeID || x.Kind != y.Kind || !reflect.DeepEqual(x.ShareID, y.ShareID) || x.Binding.Device != y.Binding.Device || x.Binding.Inode != y.Binding.Inode || x.Binding.MountID != y.Binding.MountID {
		return false
	}
	// Only explicitly qualified subvolume identities enclosing this raw path can transfer across scans.
	selectVolumes := func(m scopemanifest.ScopeManifest) []scopemanifest.QualifiedSubvolume {
		out := []scopemanifest.QualifiedSubvolume{}
		for _, q := range m.QualifiedSubvolumes {
			if q.TargetID == target && (relative == q.RelativePath || strings.HasPrefix(relative, q.RelativePath+"/")) {
				out = append(out, q)
			}
		}
		return out
	}
	return reflect.DeepEqual(selectVolumes(a), selectVolumes(b))
}

// SwitchView adopts exactly the requested published complete generation and maps selection by bound raw identity.
func (m *Manager) SwitchView(ctx context.Context, owner, profile, oldID, newID, targetSnapshot, nodeID string) (ReaderView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := ReaderView{Selection: ViewSelection{Fallback: "scope-unavailable"}}
	if owner == "" || !identifier.MatchString(profile) || !identifier.MatchString(oldID) || !identifier.MatchString(newID) || !identifier.MatchString(targetSnapshot) || (nodeID != "" && !identifier.MatchString(nodeID)) || oldID == newID {
		return out, ErrInvalid
	}
	m.readerMu.Lock()
	old := m.viewLeases[oldID]
	if old == nil || old.Owner != owner || old.Profile != profile {
		m.readerMu.Unlock()
		return out, ErrViewExpired
	}
	if old.Released || !m.viewNow().Before(old.Expires) {
		m.readerMu.Unlock()
		return out, ErrViewExpired
	}
	if m.generationReaders == nil {
		m.generationReaders = map[string]int{}
	}
	m.generationReaders[old.Snapshot]++
	m.readerMu.Unlock()
	defer func() { m.readerMu.Lock(); m.generationReaders[old.Snapshot]--; m.readerMu.Unlock() }()
	// A historical switch must not bypass the qualification required by initial historical opening.
	active, err := m.activeGeneration(ctx, profile)
	if err != nil {
		return out, err
	}
	if targetSnapshot != active {
		if err = m.qualifyRetainedTarget(ctx, profile, targetSnapshot); err != nil {
			return out, err
		}
	}
	var source selectionSource
	var virtual *query.VirtualSelection
	if nodeID != "" {
		db, close, err := m.generationDB(old.Snapshot)
		if err != nil {
			return out, ErrViewExpired
		}
		defer close()
		h, err := index.ReadGenerationHeader(ctx, db, old.Snapshot)
		if err != nil || h.Profile.ID != profile {
			return out, ErrViewExpired
		}
		source.Manifest = h.Manifest
		record, readErr := index.ReadNode(ctx, db, nodeID)
		err = readErr
		if err == nil {
			source.Target, source.Path = record.TargetID, record.Path
		}
		if errors.Is(err, sql.ErrNoRows) {
			virtual, err = query.QualifyVirtualSelection(ctx, db, h, nodeID)
			if err != nil {
				return out, err
			}
			if virtual == nil {
				return out, ErrNotFound
			}
		} else if err != nil {
			return out, err
		}

	}
	out, err = m.openViewLocked(ctx, owner, profile, newID, "", false, targetSnapshot, "switch|"+oldID+"|"+targetSnapshot+"|"+nodeID)
	if err != nil {
		return out, err
	}
	if out.View.State == nil || *out.View.State != "complete" {
		_ = m.ReleaseView(owner, profile, newID)
		return ReaderView{}, ErrInvalid
	}
	if nodeID == "" {
		out.Selection.Fallback = "none"
		return out, nil
	}
	db, close, err := m.generationDB(targetSnapshot)
	if err != nil {
		return out, ErrViewExpired
	}
	defer close()
	h, err := index.ReadGenerationHeader(ctx, db, targetSnapshot)
	if err != nil {
		return out, ErrViewExpired
	}
	out.Selection = ViewSelection{Fallback: "scope-unavailable"}
	// Virtual selections use sealed topology identity, never the physical preview table.
	if virtual != nil {
		id, resolveErr := query.ResolveVirtualSelection(ctx, db, h, virtual)
		if resolveErr != nil {
			_ = m.ReleaseView(owner, profile, newID)
			return out, resolveErr
		}
		if id != "" {
			out.Selection = ViewSelection{NodeID: &id, Fallback: "none"}
			m.readerMu.Lock()
			m.viewLeases[newID].Selected = id
			m.viewLeases[newID].Fallback = "none"
			m.readerMu.Unlock()
		}
		return out, nil
	}
	if !sameSelectionScope(source.Manifest, h.Manifest, source.Target, string(source.Path)) {
		return out, nil
	}
	relative := string(source.Path)
	for {
		record, readErr := index.FindNode(ctx, db, source.Target, []byte(relative))
		err = readErr
		id := record.Node.ID
		if err == nil {
			out.Selection.NodeID = &id
			out.Selection.Fallback = "none"
			if relative != string(source.Path) {
				out.Selection.Fallback = "ancestor"
			}
			m.readerMu.Lock()
			m.viewLeases[newID].Selected = id
			m.viewLeases[newID].Fallback = out.Selection.Fallback
			m.readerMu.Unlock()
			if err = m.includeSelected(ctx, &out, targetSnapshot, id); err != nil {
				return out, err
			}
			return out, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if relative == "." {
			return out, nil
		}
		relative = path.Dir(relative)
	}
}

// includeSelected keeps the explicitly restored node visible within the existing bounded preview.
func (m *Manager) includeSelected(ctx context.Context, out *ReaderView, snapshot, nodeID string) error {
	for _, n := range out.View.Nodes {
		if n.ID == nodeID {
			return nil
		}
	}
	db, close, err := m.generationDB(snapshot)
	if err != nil {
		return err
	}
	defer close()
	record, err := index.ReadNode(ctx, db, nodeID)
	if err != nil {
		// Renewed/replayed virtual selections belong to topology, not the physical preview.
		if errors.Is(err, sql.ErrNoRows) {
			h, headerErr := index.ReadGenerationHeader(ctx, db, snapshot)
			if headerErr != nil {
				return headerErr
			}
			virtual, virtualErr := query.QualifyVirtualSelection(ctx, db, h, nodeID)
			if virtualErr != nil {
				return virtualErr
			}
			if virtual != nil {
				return nil
			}
		}
		return err
	}
	selected := record.Node
	raw, marshalErr := json.Marshal(selected)
	if marshalErr != nil || len(raw) > 40*1024 || selected.SnapshotID != snapshot {
		return ErrUnavailable
	}
	size := len(raw)
	kept := []contracts.Node{selected}
	for _, n := range out.View.Nodes {
		encoded, _ := json.Marshal(n)
		if len(kept) >= 200 || size+len(encoded) > 40*1024 {
			out.View.Truncated = true
			break
		}
		size += len(encoded)
		kept = append(kept, n)
	}
	out.View.Nodes = kept
	return nil
}
