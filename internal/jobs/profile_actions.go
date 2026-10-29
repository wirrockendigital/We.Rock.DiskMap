package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// Action failures expose stable product states without leaking database or source details.
var (
	ErrProfileMissing  = errors.New("PROFILE_NOT_FOUND")
	ErrProfileConflict = errors.New("PROFILE_REVISION_CONFLICT")
	ErrProfileActive   = errors.New("PROFILE_JOB_ACTIVE")
	ErrActionBudget    = errors.New("ACTION_BUDGET_EXCEEDED")
)

// ProfileActionResult is an immutable replay receipt, never a mutable cleanup progress report.
type ProfileActionResult struct {
	RejectionCode      string             `json:"rejectionCode,omitempty"`
	Operation          string             `json:"operation"`
	ProfileID          string             `json:"profileId"`
	Profile            *contracts.Profile `json:"profile,omitempty"`
	CleanupScheduled   bool               `json:"cleanupScheduled"`
	TasksRequireReview bool               `json:"tasksRequireReview"`
}

// ProfileAction commits domain state and replay together; no source filesystem operation is permitted.
func (m *Manager) ProfileAction(ctx context.Context, in profiles.ActionRequest, principal, key string) (out ProfileActionResult, err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	defer func() {
		if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			err = ErrActionBudget
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return out, ErrUnavailable
	}
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return out, ErrInvalid
	}
	replay := ReplayRequest{Principal: principal, Operation: "profile." + in.Operation, Key: key, Payload: string(payload)}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	previous, found, err := LookupReplay(tx, replay)
	if err != nil {
		return out, err
	}
	if found {
		if security.DecodeJSON(bytes.NewReader(previous), 65536, &out) != nil {
			return out, ErrUnavailable
		}
		if out.RejectionCode != "" {
			if in.Operation != "create" || !validOnboardingRejection(out) {
				return out, ErrUnavailable
			}
			return out, onboardingRejectionError(out.RejectionCode)
		}
		return out, nil
	}
	var old profiles.Record
	if in.Operation != "create" {
		old, err = profiles.Read(ctx, tx, in.ProfileID)
		if errors.Is(err, profiles.ErrNotFound) {
			return out, ErrProfileMissing
		}
		if err != nil {
			return out, err
		}
		if old.Profile.Revision != in.ExpectedRevision {
			return out, ErrProfileConflict
		}
	}
	out.Operation = in.Operation
	out.ProfileID = in.ProfileID
	switch in.Operation {
	case "create", "duplicate":
		var p contracts.Profile
		if in.Operation == "create" {
			p = in.Configuration.Profile("")
		} else {
			p = old.Profile
			p.Name = in.Name
		}
		p.ID, err = profiles.NewID()
		if err != nil {
			return out, err
		}
		p.Revision = 1
		// A savepoint prevents any partial domain write from accompanying a rejected receipt.
		if _, err = tx.ExecContext(ctx, "SAVEPOINT onboarding_create"); err != nil {
			return out, err
		}
		// Public creation always resolves the current native inventory, including duplicates.
		p, err = m.createNativeProfileTx(ctx, tx, p)
		if err != nil {
			if in.Operation == "create" {
				return out, persistOnboardingRejection(ctx, tx, replay, err)
			}
			return out, err
		}
		if _, err = tx.ExecContext(ctx, "RELEASE onboarding_create"); err != nil {
			return out, err
		}
		out.ProfileID = p.ID
		out.Profile = &p
	case "update":
		// Existing isolated bootstrap fixtures never become a public physical-path authority.
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT sources_json FROM profile_sources WHERE profile_id=?`, in.ProfileID).Scan(&raw); err != nil {
			return out, err
		}
		sources, e := loadSources(raw)
		if e != nil || sources.Mode != "native_inventory" {
			return out, ErrUnavailable
		}
		p, e := m.updateProfileTx(ctx, tx, in.ProfileID, in.ExpectedRevision, in.Configuration.Profile(in.ProfileID))
		if e != nil {
			return out, e
		}
		out.Profile = &p
	case "deactivate":
		old.Profile.Active = false
		known := map[string]bool{}
		for _, id := range old.Profile.Scope.TargetIDs {
			known[id] = true
		}
		p, e := profiles.Write(ctx, tx, old, in.ExpectedRevision, known)
		if e != nil {
			return out, profileError(e)
		}
		out.Profile = &p
		out.TasksRequireReview = true
	case "delete":
		if old.Profile.Name != in.ConfirmProfileName {
			return out, ErrProfileConflict
		}
		if _, pending, e := m.pendingProfile(tx, in.ProfileID); e != nil {
			return out, e
		} else if pending {
			return out, ErrProfileActive
		}
		if err = m.scheduleProfileDeletion(ctx, tx, in.ProfileID); err != nil {
			return out, err
		}
		out.CleanupScheduled = true
		out.TasksRequireReview = true
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if err = RecordReplay(tx, replay, encoded); err != nil {
		return out, err
	}
	if err = CommitReplay(tx, nil); err != nil {
		return out, err
	}
	// Cleanup failures remain observable and durable; the committed mutation is still successful.
	if in.Operation == "delete" {
		m.cleanupProfiles(ctx)
	}
	return out, nil
}

// createNativeProfileTx captures only native catalog identities; browser input cannot supply roots.
func (m *Manager) createNativeProfileTx(ctx context.Context, tx *sql.Tx, p contracts.Profile) (contracts.Profile, error) {
	// Unsupported expert settings are rejected before catalog access or durable profile creation.
	if _, err := resources.Resolve(p.ResourceMode, p.Limits); err != nil {
		return p, ErrInvalid
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM profiles`).Scan(&count); err != nil {
		return p, err
	}
	if count >= 200 {
		return p, ErrConflict
	}
	sources, err := m.catalog(ctx, profileSources{Version: 1, Mode: "native_inventory"})
	if err != nil || sources.Mode != "native_inventory" {
		return p, ErrUnavailable
	}
	normalized, err := normalizeSources(p.Scope, sources)
	if err != nil {
		return p, ErrInvalid
	}
	known := map[string]bool{}
	for _, id := range normalized.CoveredTargetIDs {
		known[id] = true
	}
	bindings := map[string]scanner.RootBinding{}
	volumes := map[string]string{}
	for _, r := range normalized.Roots {
		known[r.TargetID] = true
		if !safeSourcePath(r.PhysicalPath, m.state, false) {
			return p, ErrInvalid
		}
		volumes[r.TargetID] = r.VolumeID
		if r.State != "reachable" {
			continue
		}
		fd, b, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return p, ErrUnavailable
		}
		fd.Close()
		bindings[r.TargetID] = b
	}
	if err = confirmNativeBindings(ctx, p, sources, bindings, m.catalog); err != nil {
		return p, err
	}
	stampNativeBindings(&sources, p, bindings)
	rootsJSON, _ := json.Marshal(bindings)
	volumesJSON, _ := json.Marshal(volumes)
	p, err = profiles.Write(ctx, tx, profiles.Record{Profile: p, RootsJSON: string(rootsJSON), VolumesJSON: string(volumesJSON)}, 0, known)
	if err != nil {
		return p, profileError(err)
	}
	sourceJSON, _ := json.Marshal(sources)
	_, err = tx.ExecContext(ctx, `INSERT INTO profile_sources(profile_id,sources_json) VALUES(?,?)`, p.ID, string(sourceJSON))
	return p, err
}
