package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

var ErrOnboardingConflict = errors.New("ONBOARDING_CONFLICT")
var ErrOnboardingPending = errors.New("ONBOARDING_PENDING")

// OnboardingCreate captures the existing create domain, not an alternative action engine.
type OnboardingCreate struct {
	Key     string                 `json:"key"`
	Request profiles.ActionRequest `json:"request"`
}
type OnboardingChange struct {
	Operation string                     `json:"operation"`
	Draft     *contracts.OnboardingDraft `json:"draft,omitempty"`
	Create    *OnboardingCreate          `json:"create,omitempty"`
}
type OnboardingRequest struct {
	ContractVersion  int              `json:"contractVersion"`
	ExpectedRevision int              `json:"expectedRevision"`
	Change           OnboardingChange `json:"change"`
}

// PendingOnboardingCreate persists only the exact canonical action and its original key.
type PendingOnboardingCreate struct {
	Key              string `json:"key"`
	CanonicalPayload string `json:"canonicalPayload"`
}
type OnboardingState struct {
	Version  int                        `json:"version"`
	Revision int                        `json:"revision"`
	Draft    *contracts.OnboardingDraft `json:"draft"`
	Pending  *PendingOnboardingCreate   `json:"pendingProfileCreate"`
}
type OnboardingRecovery struct {
	RejectionCode    string               `json:"rejectionCode,omitempty"`
	Key              string               `json:"key"`
	CanonicalPayload string               `json:"canonicalPayload"`
	Status           string               `json:"status"`
	Receipt          *ProfileActionResult `json:"receipt"`
	ProfileState     string               `json:"profileState"`
}

// OnboardingResponse distinguishes durable intent from a fresh read-only recovery observation.
type OnboardingResponse struct {
	Version      int                        `json:"version"`
	Revision     int                        `json:"revision"`
	Draft        *contracts.OnboardingDraft `json:"draft"`
	Pending      *OnboardingRecovery        `json:"pendingProfileCreate"`
	ProfileState string                     `json:"profileState"`
}

// validateOnboardingDraft allows unfinished form values while bounding every typed collection.
func validateOnboardingDraft(d *contracts.OnboardingDraft) bool {
	if d == nil {
		return true
	}
	if d.Step < 1 || d.Step > 6 || !oneOf(d.Language, "auto", "de", "en") || !oneOf(d.UpdateChoice, "undecided", "manual", "dsm-help") || d.ProfileID != nil && !security.ValidID(*d.ProfileID) {
		return false
	}
	c := d.Configuration
	if c == nil {
		return true
	}
	if c.Name != nil && (!utf8.ValidString(*c.Name) || len(*c.Name) > 1024 || strings.ContainsRune(*c.Name, 0)) {
		return false
	}
	if c.Scope != nil {
		if !oneOf(c.Scope.Kind, "", "nas", "volumes", "shares", "mixed") || len(c.Scope.TargetIDs) > 256 {
			return false
		}
		seen := map[string]bool{}
		for _, id := range c.Scope.TargetIDs {
			if !security.ValidID(id) || seen[id] {
				return false
			}
			seen[id] = true
		}
	}
	if c.Exclusions != nil {
		if len(*c.Exclusions) > 128 {
			return false
		}
		for _, r := range *c.Exclusions {
			if !oneOf(r.Kind, "path", "pattern") || len(r.Value) > 4096 || !utf8.ValidString(r.Value) || strings.ContainsRune(r.Value, 0) {
				return false
			}
		}
	}
	if c.ResourceMode != nil && !oneOf(*c.ResourceMode, "", "gentle", "balanced", "fast", "custom") {
		return false
	}
	if c.Limits != nil && (c.Limits.Workers < 0 || c.Limits.Workers > 64 || c.Limits.CPUPercent != nil && (*c.Limits.CPUPercent < 0 || *c.Limits.CPUPercent > 100) || c.Limits.IOBytesPerSecond != nil && c.Limits.IOBytesPerSecond.Validate() != nil) {
		return false
	}
	if c.FreshnessSeconds != nil && (*c.FreshnessSeconds < 0 || *c.FreshnessSeconds > 2147483647) {
		return false
	}
	raw, e := json.Marshal(d)
	return e == nil && len(raw) <= 24576
}

// Validate enforces a strict tagged change and preserves the existing 16-KiB profile action boundary.
func (r OnboardingRequest) Validate() error {
	if r.ContractVersion != 1 || r.ExpectedRevision < 0 || r.ExpectedRevision >= MaxPreferenceRevision {
		return ErrInvalid
	}
	c := r.Change
	switch c.Operation {
	case "replace":
		if c.Draft == nil || c.Create != nil || !validateOnboardingDraft(c.Draft) {
			return ErrInvalid
		}
	case "prepareProfileCreate":
		if c.Draft != nil || c.Create == nil || !identifier.MatchString(c.Create.Key) || c.Create.Request.Operation != "create" || c.Create.Request.Validate() != nil {
			return ErrInvalid
		}
		raw, e := json.Marshal(c.Create.Request)
		if e != nil || len(raw) > 16384 {
			return ErrInvalid
		}
	case "acknowledgeProfileCreate", "acknowledgeProfileRejection", "reset":
		if c.Draft != nil || c.Create != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 32768 {
		return ErrInvalid
	}
	return nil
}

// decodeOnboardingCreate verifies stored canonical bytes before they may be repeated or reconciled.
func decodeOnboardingCreate(p *PendingOnboardingCreate) (profiles.ActionRequest, error) {
	var in profiles.ActionRequest
	if p == nil || !identifier.MatchString(p.Key) || security.DecodeJSON(strings.NewReader(p.CanonicalPayload), 16384, &in) != nil || in.Operation != "create" || in.Validate() != nil {
		return in, ErrUnavailable
	}
	raw, _ := json.Marshal(in)
	if string(raw) != p.CanonicalPayload {
		return in, ErrUnavailable
	}
	return in, nil
}
func validOnboardingState(s OnboardingState) bool {
	if s.Version != 1 || s.Revision < 0 || s.Revision > MaxPreferenceRevision || !validateOnboardingDraft(s.Draft) {
		return false
	}
	if s.Revision == 0 && (s.Draft != nil || s.Pending != nil) {
		return false
	}
	if s.Pending != nil {
		if s.Draft == nil || s.Draft.ProfileID != nil {
			return false
		}
		if _, e := decodeOnboardingCreate(s.Pending); e != nil {
			return false
		}
	}
	raw, e := json.Marshal(s)
	return e == nil && len(raw) <= 65536
}

// A missing principal row is the only legitimate empty state.
func readOnboarding(ctx context.Context, tx *sql.Tx, principal string) (OnboardingState, error) {
	empty := OnboardingState{Version: 1}
	var revision int
	var raw string
	e := tx.QueryRowContext(ctx, "SELECT revision,value_json FROM onboarding_drafts WHERE principal=?", principal).Scan(&revision, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return empty, nil
	}
	if e != nil {
		return empty, ErrUnavailable
	}
	var s OnboardingState
	if security.DecodeJSON(strings.NewReader(raw), 65536, &s) != nil || !validOnboardingState(s) || s.Revision != revision {
		return empty, ErrUnavailable
	}
	return s, nil
}

// profileState reads current identity only; missing profiles never become name-based replacements.
func onboardingProfileState(ctx context.Context, tx *sql.Tx, id *string) (string, error) {
	if id == nil {
		return "none", nil
	}
	r, e := profiles.Read(ctx, tx, *id)
	if errors.Is(e, profiles.ErrNotFound) {
		return "missing", nil
	}
	if e != nil {
		return "", ErrUnavailable
	}
	if r.Profile.Active {
		return "active", nil
	}
	return "inactive", nil
}
func reconcileOnboarding(ctx context.Context, tx *sql.Tx, principal string, s OnboardingState) (OnboardingResponse, error) {
	out := OnboardingResponse{Version: s.Version, Revision: s.Revision, Draft: s.Draft, ProfileState: "none"}
	if s.Draft != nil {
		state, e := onboardingProfileState(ctx, tx, s.Draft.ProfileID)
		if e != nil {
			return out, e
		}
		out.ProfileState = state
	}
	if s.Pending == nil {
		return out, nil
	}
	in, e := decodeOnboardingCreate(s.Pending)
	if e != nil {
		return out, e
	}
	out.Pending = &OnboardingRecovery{Key: s.Pending.Key, CanonicalPayload: s.Pending.CanonicalPayload, Status: "pending", ProfileState: "unknown"}
	raw, found, e := LookupReplay(tx, ReplayRequest{principal, "profile.create", s.Pending.Key, s.Pending.CanonicalPayload})
	if e != nil {
		return out, e
	}
	if !found {
		return out, nil
	}
	var receipt ProfileActionResult
	if security.DecodeJSON(bytes.NewReader(raw), 65536, &receipt) != nil {
		return out, ErrUnavailable
	}
	if receipt.RejectionCode != "" {
		if !validOnboardingRejection(receipt) {
			return out, ErrUnavailable
		}
		out.Pending.Status = "rejected"
		out.Pending.RejectionCode = receipt.RejectionCode
		return out, nil
	}
	if security.DecodeJSON(bytes.NewReader(raw), 65536, &receipt) != nil || receipt.Operation != "create" || receipt.Profile == nil || receipt.ProfileID != receipt.Profile.ID || !security.ValidID(receipt.ProfileID) || receipt.Profile.Revision != 1 || receipt.CleanupScheduled || receipt.TasksRequireReview {
		return out, ErrUnavailable
	}
	// The existing create domain preserves submitted configuration; a receipt must attest all fields.
	expected := in.Configuration.Profile(receipt.ProfileID)
	expected.Revision = 1
	if !reflect.DeepEqual(expected, *receipt.Profile) {
		return out, ErrUnavailable
	}
	known := map[string]bool{}
	for _, id := range receipt.Profile.Scope.TargetIDs {
		known[id] = true
	}
	if profiles.Validate(*receipt.Profile, known) != nil {
		return out, ErrUnavailable
	}
	state, e := onboardingProfileState(ctx, tx, &receipt.ProfileID)
	if e != nil {
		return out, e
	}
	if state == "active" {
		current, err := profiles.Read(ctx, tx, receipt.ProfileID)
		if err != nil {
			return out, ErrUnavailable
		}
		if !reflect.DeepEqual(current.Profile, *receipt.Profile) {
			state = "changed"
		}
	}
	out.Pending.Receipt = &receipt
	out.Pending.ProfileState = state
	out.Pending.Status = "committed"
	if state == "missing" || state == "inactive" || state == "changed" {
		out.Pending.Status = "profile-unavailable"
	}
	return out, nil
}

// OnboardingDraft performs recovery observation only, never a create or a scan.
func (m *Manager) OnboardingDraft(parent context.Context, principal string) (OnboardingResponse, error) {
	if !validPrincipal(principal) {
		return OnboardingResponse{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return OnboardingResponse{}, ErrUnavailable
	}
	defer tx.Rollback()
	s, e := readOnboarding(ctx, tx, principal)
	if e != nil {
		return OnboardingResponse{}, e
	}
	out, e := reconcileOnboarding(ctx, tx, principal, s)
	if e == nil && !ValidOnboardingResponse(out) {
		return OnboardingResponse{}, ErrUnavailable
	}
	return out, e
}

// SetOnboardingDraft atomically stores a revision and its exact receipt; it never invokes ProfileAction.
func (m *Manager) SetOnboardingDraft(parent context.Context, in OnboardingRequest, principal, key string) (OnboardingState, error) {
	var zero OnboardingState
	if in.Validate() != nil || !validPrincipal(principal) {
		return zero, ErrInvalid
	}
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{principal, "onboarding.update", key, string(raw)}
	if replay.Validate() != nil {
		return zero, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return zero, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback()
	prev, found, e := LookupReplay(tx, replay)
	if e != nil {
		return zero, e
	}
	if found {
		if security.DecodeJSON(bytes.NewReader(prev), 65536, &zero) != nil || !ValidOnboardingReceipt(zero, in) {
			return OnboardingState{}, ErrUnavailable
		}
		return zero, nil
	}
	s, e := readOnboarding(ctx, tx, principal)
	if e != nil {
		return zero, e
	}
	if s.Revision != in.ExpectedRevision {
		return zero, ErrOnboardingConflict
	}
	switch in.Change.Operation {
	case "replace":
		if s.Pending != nil {
			return zero, ErrOnboardingPending
		}
		var old *string
		if s.Draft != nil {
			old = s.Draft.ProfileID
		}
		if !reflect.DeepEqual(old, in.Change.Draft.ProfileID) {
			return zero, ErrInvalid
		}
		s.Draft = in.Change.Draft
	case "reset":
		if s.Pending != nil {
			return zero, ErrOnboardingPending
		}
		s.Draft = nil
	case "prepareProfileCreate":
		if s.Pending != nil {
			return zero, ErrOnboardingPending
		}
		if s.Draft == nil || s.Draft.ProfileID != nil {
			return zero, ErrInvalid
		}
		p, _ := json.Marshal(in.Change.Create.Request)
		// Reject an already-used create key with different bytes before installing an unresolved intent.
		if _, _, e := LookupReplay(tx, ReplayRequest{principal, "profile.create", in.Change.Create.Key, string(p)}); e != nil {
			return zero, e
		}
		s.Pending = &PendingOnboardingCreate{in.Change.Create.Key, string(p)}
	case "acknowledgeProfileRejection":
		if s.Pending == nil {
			return zero, ErrOnboardingPending
		}
		observed, err := reconcileOnboarding(ctx, tx, principal, s)
		if err != nil {
			return zero, err
		}
		if observed.Pending == nil || observed.Pending.Status != "rejected" {
			return zero, ErrOnboardingPending
		}
		// Only a durable rejection frees the draft; the create replay remains permanently reserved.
		s.Pending = nil
	case "acknowledgeProfileCreate":
		if s.Pending == nil {
			return zero, ErrInvalid
		}
		observed, e := reconcileOnboarding(ctx, tx, principal, s)
		if e != nil {
			return zero, e
		}
		if observed.Pending.Receipt == nil {
			return zero, ErrOnboardingPending
		}
		id := observed.Pending.Receipt.ProfileID
		s.Draft.ProfileID = &id
		s.Pending = nil
	}
	s.Revision++
	if !validOnboardingState(s) || !onboardingRecoveryFits(s) {
		return zero, ErrInvalid
	}
	value, _ := json.Marshal(s)
	if _, e = tx.ExecContext(ctx, `INSERT INTO onboarding_drafts(principal,revision,value_json)VALUES(?,?,?) ON CONFLICT(principal)DO UPDATE SET revision=excluded.revision,value_json=excluded.value_json`, principal, s.Revision, string(value)); e != nil {
		return zero, e
	}
	if e = RecordReplay(tx, replay, value); e != nil {
		return zero, e
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return s, nil
}

// migrateOnboarding runs only inside a fresh or offline candidate database.
func migrateOnboarding(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var name string
	if tx.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=5").Scan(&name) != nil || name != "exclusion-template-v1" {
		return ErrUnavailable
	}
	_, e = tx.ExecContext(ctx, `CREATE TABLE onboarding_drafts(principal TEXT PRIMARY KEY,revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 2147483647),value_json TEXT NOT NULL CHECK(length(CAST(value_json AS BLOB))<=65536));INSERT INTO schema_steps VALUES(6,'onboarding-draft-v1');PRAGMA user_version=6`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
