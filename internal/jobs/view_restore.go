package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// RestoreRequest uses a new bounded lease intent, never a persisted bearer capability.
type RestoreRequest struct {
	ContractVersion            int    `json:"contractVersion"`
	ViewID                     string `json:"viewId"`
	ExpectedPreferenceRevision int    `json:"expectedPreferenceRevision"`
}

// Validate checks the bounded acquisition intent before lifecycle state is read.
func (r RestoreRequest) Validate() error {
	if r.ContractVersion != 1 || !identifier.MatchString(r.ViewID) || r.ExpectedPreferenceRevision < 0 || r.ExpectedPreferenceRevision > MaxPreferenceRevision {
		return ErrInvalid
	}
	return nil
}

// RestoreResult keeps an existing empty profile distinct from the absence of any profile.
type RestoreResult struct {
	PreferenceRevision int                       `json:"preferenceRevision"`
	ProfileID          *string                   `json:"profileId"`
	ResolvedView       *contracts.PreferenceView `json:"resolvedView"`
	Reader             *ReaderView               `json:"reader"`
	Navigation         *query.Navigation         `json:"navigation"`
	Reasons            []string                  `json:"reasons"`
}

// NavigationRequest repeats the immutable snapshot binding on every read.
type NavigationRequest struct {
	ContractVersion int     `json:"contractVersion"`
	ProfileID       string  `json:"profileId"`
	ViewID          string  `json:"viewId"`
	SnapshotID      string  `json:"snapshotId"`
	RootID          string  `json:"rootId"`
	SelectedNodeID  *string `json:"selectedNodeId"`
}

// Validate rejects paths and ambiguous identities before reserving a reader.
func (r NavigationRequest) Validate() error {
	if r.ContractVersion != 1 || !identifier.MatchString(r.ProfileID) || !identifier.MatchString(r.ViewID) || !identifier.MatchString(r.SnapshotID) || !identifier.MatchString(r.RootID) || (r.SelectedNodeID != nil && !identifier.MatchString(*r.SelectedNodeID)) {
		return ErrInvalid
	}
	return nil
}

// ReadNavigation shares query-slot and reader-lifetime guards with hierarchy requests.
func (m *Manager) ReadNavigation(ctx context.Context, owner string, in NavigationRequest) (query.Navigation, error) {
	if in.Validate() != nil {
		return query.Navigation{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	req := HierarchyRequest{ContractVersion: 1, ProfileID: in.ProfileID, ViewID: in.ViewID, Query: query.Request{SnapshotID: in.SnapshotID, RootID: in.RootID, Metric: "logical", Depth: 1, ElementBudget: 50, Filter: query.Filter{Categories: []string{}}}}
	release, err := m.reserveHierarchy(ctx, owner, req)
	if err != nil {
		return query.Navigation{}, err
	}
	defer release()
	return m.navigation(ctx, in.ProfileID, in.SnapshotID, in.RootID, in.SelectedNodeID)
}

// navigation opens only a qualified held generation; callers already protect its reader lifetime.
func (m *Manager) navigation(ctx context.Context, profile, snapshot, root string, selected *string) (query.Navigation, error) {
	db, close, err := m.generationDB(snapshot)
	if err != nil {
		return query.Navigation{}, err
	}
	defer close()
	h, err := index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		return query.Navigation{}, err
	}
	if h.Profile.ID != profile {
		return query.Navigation{}, query.ErrSnapshot
	}
	return query.ReadNavigation(ctx, db, h, root, selected)
}

// RestoreView never rewrites preferences and never transfers opaque node IDs to another generation.
func (m *Manager) RestoreView(ctx context.Context, owner string, in RestoreRequest) (RestoreResult, error) {
	out := RestoreResult{Reasons: []string{}}
	if in.Validate() != nil || !validPrincipal(owner) {
		return out, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return out, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout)
	defer cancel()
	pref, err := readPreferences(ctx, m.db, owner)
	if err != nil {
		return out, err
	}
	out.PreferenceRevision = pref.Revision
	if pref.Revision != in.ExpectedPreferenceRevision {
		return out, ErrPreferenceConflict
	}
	catalog, err := profiles.List(ctx, m.db)
	if err != nil {
		return out, err
	}
	desired := ""
	if pref.PreferredProfileID != nil {
		desired = *pref.PreferredProfileID
	}
	// A configured start profile wins; only the automatic mode follows the last view.
	if pref.PreferredProfileID == nil && pref.LastView != nil {
		desired = pref.LastView.ProfileID
	}
	chosen := ""
	for _, p := range catalog {
		if p.Profile.ID == desired {
			chosen = desired
			break
		}
	}
	if chosen == "" {
		if desired != "" {
			out.Reasons = append(out.Reasons, "PROFILE_UNAVAILABLE")
		}
		if len(catalog) == 0 {
			out.Reasons = append(out.Reasons, "NO_PROFILE")
			return out, nil
		}
		chosen = catalog[0].Profile.ID
	}
	out.ProfileID = &chosen
	intent := "restore|" + strconv.Itoa(pref.Revision)
	// An existing intent pins the first acquired generation even if the active pointer subsequently changes.
	m.readerMu.Lock()
	var lease *viewLease
	if current := m.viewLeases[in.ViewID]; current != nil {
		copy := *current
		lease = &copy
	}
	m.readerMu.Unlock()
	fixed := ""
	remembered := pref.LastView != nil && pref.LastView.ProfileID == chosen
	if lease != nil {
		if lease.Owner != owner || lease.Profile != chosen {
			return out, ErrNotFound
		}
		if lease.Intent != intent {
			return out, ErrIdempotencyConflict
		}
		if lease.Released || !m.viewNow().Before(lease.Expires) {
			return out, ErrViewExpired
		}
		fixed = lease.Snapshot
	} else if remembered {
		active, e := m.activeGeneration(ctx, chosen)
		if e != nil {
			return out, e
		}
		fixed = pref.LastView.SnapshotID
		if fixed != active {
			if e = m.qualifyRetainedTarget(ctx, chosen, fixed); e != nil {
				if ctx.Err() != nil {
					return out, ctx.Err()
				}
				fixed = ""
				out.Reasons = append(out.Reasons, "SNAPSHOT_UNAVAILABLE")
			}
		}
	}
	reader, err := m.openViewLocked(ctx, owner, chosen, in.ViewID, "", false, fixed, intent)
	if err != nil && lease == nil && remembered && fixed != "" && (errors.Is(err, ErrNotFound) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrGenerationRefresh)) {
		fixed = ""
		out.Reasons = append(out.Reasons, "SNAPSHOT_UNAVAILABLE")
		reader, err = m.openViewLocked(ctx, owner, chosen, in.ViewID, "", false, "", intent)
	}
	if err != nil {
		return out, err
	}
	if reader.Lease == nil || reader.View.SnapshotID == nil {
		out.Reasons = append(out.Reasons, "NO_SNAPSHOT")
		return out, nil
	}
	failed := true
	defer func() {
		if failed && lease == nil {
			_ = m.ReleaseView(owner, chosen, in.ViewID)
		}
	}()
	// Newly acquired leases protect the immutable generation during this bounded metadata read.
	snapshot := *reader.View.SnapshotID
	view := contracts.PreferenceView{ProfileID: chosen, SnapshotID: snapshot, RootID: "nas", Map: pref.Map, Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "name", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	if remembered {
		view = *pref.LastView
		if view.SnapshotID != snapshot {
			// Selectors remain valid preferences; only generation-bound identities are discarded.
			view.SnapshotID = snapshot
			view.RootID = "nas"
			view.SelectedNodeID = nil
		}
	}
	nav, err := m.navigation(ctx, chosen, snapshot, view.RootID, view.SelectedNodeID)
	if err != nil {
		return out, err
	}
	view.RootID = nav.RootID
	view.SelectedNodeID = nav.SelectedNodeID
	out.Reader = &reader
	out.ResolvedView = &view
	out.Navigation = &nav
	out.Reasons = append(out.Reasons, nav.Reasons...)
	// Only the explicitly partial preview may shrink; authoritative preference and navigation data remain intact.
	for {
		raw, marshalErr := json.Marshal(contracts.Envelope[RestoreResult]{ContractVersion: 1, RequestID: "view", Data: out})
		if marshalErr != nil {
			return RestoreResult{}, ErrUnavailable
		}
		if len(raw) <= 60*1024 {
			break
		}
		if len(out.Reader.View.Nodes) == 0 {
			return RestoreResult{}, query.ErrBudget
		}
		out.Reader.View.Nodes = out.Reader.View.Nodes[:len(out.Reader.View.Nodes)/2]
		out.Reader.View.Truncated = true
	}
	failed = false
	return out, nil
}
