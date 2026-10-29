package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"path/filepath"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// profileError maps persistence failures onto existing private API error classes.
func profileError(err error) error {
	switch {
	case errors.Is(err, profiles.ErrExclusionSyntax), errors.Is(err, profiles.ErrInvalid), errors.Is(err, profiles.ErrNotFound), errors.Is(err, profiles.ErrRetired):
		return ErrInvalid
	case errors.Is(err, profiles.ErrConflict), errors.Is(err, profiles.ErrInUse):
		return ErrConflict
	}
	return err
}

// scanProfileSupported prevents persisted future options from silently changing scan semantics.
func scanProfileSupported(p contracts.Profile) bool {
	_, err := profiles.CompileExclusions(p.Exclusions)
	_, policyErr := resources.Resolve(p.ResourceMode, p.Limits)
	return err == nil && p.Active && policyErr == nil
}

// RenameProfile changes presentation only; captured jobs and generations retain their original name.
func (m *Manager) RenameProfile(ctx context.Context, id string, revision int, name string) (contracts.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var empty contracts.Profile
	if m.closed {
		return empty, ErrUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	r, err := profiles.Read(ctx, tx, id)
	if err != nil {
		return empty, profileError(err)
	}
	known := map[string]bool{}
	for _, target := range r.Profile.Scope.TargetIDs {
		known[target] = true
	}
	r.Profile.Name = name
	p, err := profiles.Write(ctx, tx, r, revision, known)
	if err != nil {
		return empty, profileError(err)
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return p, nil
}

// UpdateProfile replaces configuration by stable ID without changing a running job's captured inputs.
// Source mappings are resolved only from the existing authoritative catalog, never from request paths.
func (m *Manager) UpdateProfile(ctx context.Context, id string, expected int, p contracts.Profile) (contracts.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var empty contracts.Profile
	if m.closed || m.ctx.Err() != nil {
		return empty, ErrUnavailable
	}
	if id != p.ID || expected < 1 {
		return empty, ErrInvalid
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	next, err := m.updateProfileTx(ctx, tx, id, expected, p)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return next, nil
}

// updateProfileTx shares authoritative binding and revision checks with public transactional actions.
func (m *Manager) updateProfileTx(ctx context.Context, tx *sql.Tx, id string, expected int, p contracts.Profile) (contracts.Profile, error) {
	var empty contracts.Profile
	old, err := profiles.Read(ctx, tx, id)
	if err != nil {
		return empty, profileError(err)
	}
	if old.Profile.Revision != expected {
		return empty, ErrConflict
	}
	// Reject unsupported resource edits before inventory I/O, independently of catalog availability.
	if _, err := resources.Resolve(p.ResourceMode, p.Limits); err != nil {
		return empty, ErrInvalid
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT sources_json FROM profile_sources WHERE profile_id=?`, id).Scan(&raw); err != nil {
		return empty, err
	}
	sources, err := loadSources(raw)
	if err != nil {
		return empty, err
	}
	previous := sources
	if sources.Mode == "native_inventory" {
		sources, err = m.catalog(ctx, sources)
		if err != nil {
			return empty, ErrUnavailable
		}
		sources = retainScopeIdentities(previous, sources)
		sources.BindingMounts = copyMountProofs(previous.BindingMounts)
		sources.BindingBoots = bindingEpochs(previous.BindingBoots)
		sources.BindingIdentities = bindingEpochs(previous.BindingIdentities)
	}
	normalized, err := normalizeSources(p.Scope, sources)
	if err != nil {
		return empty, ErrInvalid
	}
	known := map[string]bool{}
	for _, id := range normalized.CoveredTargetIDs {
		known[id] = true
	}
	for _, r := range normalized.Roots {
		known[r.TargetID] = true
	}
	if profiles.Validate(p, known) != nil {
		return empty, ErrInvalid
	}
	var bindings map[string]scanner.RootBinding
	var volumes map[string]string
	if json.Unmarshal([]byte(old.RootsJSON), &bindings) != nil || json.Unmarshal([]byte(old.VolumesJSON), &volumes) != nil || bindings == nil || volumes == nil {
		return empty, ErrUnavailable
	}
	oldMappings := observations(previous)
	for _, r := range normalized.Roots {
		if !safeSourcePath(r.PhysicalPath, m.state, sources.Mode == "isolated_roots") {
			return empty, ErrInvalid
		}
		if prior, exists := oldMappings[r.TargetID]; exists && (prior.volume != r.VolumeID || filepath.Join(prior.path, r.RelativePath) != r.PhysicalPath || prior.kind != r.Kind) {
			return empty, ErrRootRebind
		}
		if signature := previous.BindingIdentities[r.TargetID]; signature != "" && r.State == "reachable" && signature != nativeRootIdentity(sources, r) {
			return empty, ErrRootRebind
		}
		if r.State != "reachable" {
			continue
		}
		fd, binding, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return empty, ErrUnavailable
		}
		fd.Close()
		if !mountAuthorityUnchanged(previous, sources, r, binding) {
			return empty, ErrRootRebind
		}
		if prior, bound := bindings[r.TargetID]; bound && prior != binding && !mayRebind(previous, sources, r, false) && !mayRemount(previous, sources, r, binding) {
			return empty, ErrRootRebind
		}
		bindings[r.TargetID] = binding
		if sources.Mode == "native_inventory" && validBoot(sources.BootID) {
			if _, had := oldMappings[r.TargetID]; !had || mayRebind(previous, sources, r, false) || mayRemount(previous, sources, r, binding) {
				sources.BindingBoots[r.TargetID] = sources.BootID
				sources.BindingIdentities[r.TargetID] = nativeRootIdentity(sources, r)
			}
		}
		stampMountProof(&sources, r, binding)
		volumes[r.TargetID] = r.VolumeID
	}
	if err = confirmNativeBindings(ctx, p, sources, bindings, m.catalog); err != nil {
		return empty, err
	}
	rootsJSON, _ := json.Marshal(bindings)
	volumesJSON, _ := json.Marshal(volumes)
	next, err := profiles.Write(ctx, tx, profiles.Record{Profile: p, RootsJSON: string(rootsJSON), VolumesJSON: string(volumesJSON)}, expected, known)
	if err != nil {
		return empty, profileError(err)
	}
	sourceJSON, _ := json.Marshal(sources)
	if _, err = tx.ExecContext(ctx, `UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(sourceJSON), id); err != nil {
		return empty, err
	}
	return next, nil
}

// detachOutcome removes all shared slices and pointers at the manager's outward boundary.
func detachOutcome(o Outcome) (Outcome, error) {
	var copy Outcome
	raw, err := json.Marshal(o)
	if err != nil {
		return copy, err
	}
	err = json.Unmarshal(raw, &copy)
	return copy, err
}
