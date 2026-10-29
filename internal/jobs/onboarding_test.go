package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// A lost create response is recovered solely through its original principal/key/bytes receipt.
func TestOnboardingCreateRecovery(t *testing.T) {
	m, in, _, catalog := nativeFixture(t, "volumes")
	ctx := context.Background()
	draft := &contracts.OnboardingDraft{Step: 2, Language: "de", UpdateChoice: "undecided"}
	put := OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: draft}}
	first, e := m.SetOnboardingDraft(ctx, put, "admin", "draft")
	if e != nil || first.Revision != 1 {
		t.Fatal(first, e)
	}
	action := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	prep := OnboardingRequest{ContractVersion: 1, ExpectedRevision: 1, Change: OnboardingChange{Operation: "prepareProfileCreate", Create: &OnboardingCreate{Key: "original-create", Request: action}}}
	saved, e := m.SetOnboardingDraft(ctx, prep, "admin", "prepare")
	if e != nil {
		t.Fatal(e)
	}
	pending, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || pending.Pending.Status != "pending" || pending.Pending.Receipt != nil {
		t.Fatal(pending, e)
	}
	for _, op := range []string{"reset", "replace", "acknowledgeProfileCreate"} {
		r := OnboardingRequest{ContractVersion: 1, ExpectedRevision: 2, Change: OnboardingChange{Operation: op}}
		if op == "replace" {
			r.Change.Draft = draft
		}
		if _, e = m.SetOnboardingDraft(ctx, r, "admin", "blocked-"+op); !errors.Is(e, ErrOnboardingPending) {
			t.Fatal(op, e)
		}
	}
	other, e := m.OnboardingDraft(ctx, "other")
	if e != nil || other.Revision != 0 || other.Pending != nil {
		t.Fatal(other, e)
	}
	created, e := m.ProfileAction(ctx, action, "admin", "original-create")
	if e != nil {
		t.Fatal(e)
	}
	state := m.state
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m, e = Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	got, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || got.Pending.Status != "committed" || got.Pending.Receipt.ProfileID != created.ProfileID {
		t.Fatal(got, e)
	}
	replay, e := m.SetOnboardingDraft(ctx, prep, "admin", "prepare")
	if e != nil || !reflect.DeepEqual(replay, saved) {
		t.Fatal(replay, e)
	}
	retry, e := m.ProfileAction(ctx, action, "admin", "original-create")
	if e != nil || retry.ProfileID != created.ProfileID {
		t.Fatal(retry, e)
	}
	off := profiles.ActionRequest{ContractVersion: 1, Operation: "deactivate", ProfileID: created.ProfileID, ExpectedRevision: 1}
	if _, e = m.ProfileAction(ctx, off, "admin", "off"); e != nil {
		t.Fatal(e)
	}
	got, e = m.OnboardingDraft(ctx, "admin")
	if e != nil || got.Pending.Status != "profile-unavailable" || got.Pending.ProfileState != "inactive" {
		t.Fatal(got, e)
	}
	ack := OnboardingRequest{ContractVersion: 1, ExpectedRevision: 2, Change: OnboardingChange{Operation: "acknowledgeProfileCreate"}}
	accepted, e := m.SetOnboardingDraft(ctx, ack, "admin", "ack")
	if e != nil || accepted.Pending != nil || *accepted.Draft.ProfileID != created.ProfileID {
		t.Fatal(accepted, e)
	}
	var count int
	m.db.QueryRow("SELECT count(*) FROM jobs").Scan(&count)
	if count != 0 {
		t.Fatal("recovery started job")
	}
}

// CAS, replay payload conflicts and corruption fail without overwriting another window's state.
func TestOnboardingCASAndBounds(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	d := &contracts.OnboardingDraft{Step: 1, Language: "auto", UpdateChoice: "undecided"}
	r := OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: d}}
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "a"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "b"); !errors.Is(e, ErrOnboardingConflict) {
		t.Fatal(e)
	}
	d.Step = 2
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "a"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(map[string]string{"value": strings.Repeat("x", 17000)})
	for _, op := range []string{"profile.create", "preferences.update", "onboarding.update"} {
		e := (ReplayRequest{"admin", op, "k", string(raw)}).Validate()
		if (e == nil) != (op == "onboarding.update") {
			t.Fatal(op, e)
		}
	}
	if (ReplayRequest{"admin", "onboarding.update", "k", strings.Repeat("x", 32769)}).Validate() == nil {
		t.Fatal("oversize accepted")
	}
	if _, e := m.db.Exec("UPDATE onboarding_drafts SET value_json='{}' WHERE principal='admin'"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.OnboardingDraft(ctx, "admin"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

// A replay write failure rolls back the draft, and concurrent windows have exactly one CAS winner.
func TestOnboardingAtomicReplay(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	r := OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: &contracts.OnboardingDraft{Step: 1, Language: "auto", UpdateChoice: "undecided"}}}
	if _, e := m.db.Exec("CREATE TRIGGER fail_onboarding_replay BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "first"); e == nil {
		t.Fatal("injected write succeeded")
	}
	out, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || out.Revision != 0 {
		t.Fatal(out, e)
	}
	m.db.Exec("DROP TRIGGER fail_onboarding_replay")
	results := make(chan error, 2)
	for _, key := range []string{"a", "b"} {
		go func(key string) { _, e := m.SetOnboardingDraft(ctx, r, "admin", key); results <- e }(key)
	}
	a, b := <-results, <-results
	if !((a == nil && errors.Is(b, ErrOnboardingConflict)) || (b == nil && errors.Is(a, ErrOnboardingConflict))) {
		t.Fatal(a, b)
	}
}

// Strict recovery validation rejects a valid-looking receipt for another configuration or scope.
func TestOnboardingRecoveryValidation(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	draft := &contracts.OnboardingDraft{Step: 3, Language: "en", UpdateChoice: "manual"}
	m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: draft}}, "admin", "save")
	action := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, ExpectedRevision: 1, Change: OnboardingChange{Operation: "prepareProfileCreate", Create: &OnboardingCreate{Key: "create", Request: action}}}, "admin", "prepare")
	created, e := m.ProfileAction(ctx, action, "admin", "create")
	if e != nil {
		t.Fatal(e)
	}
	good, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || !ValidOnboardingResponse(good) {
		t.Fatal(good, e)
	}
	raw, _ := json.Marshal(good)
	for _, field := range []string{"scope", "name", "pending-without-draft"} {
		var bad OnboardingResponse
		json.Unmarshal(raw, &bad)
		switch field {
		case "scope":
			bad.Pending.Receipt.Profile.Scope.TargetIDs = []string{"foreign"}
		case "name":
			bad.Pending.Receipt.Profile.Name = "other"
		case "pending-without-draft":
			bad.Draft = nil
		}
		if ValidOnboardingResponse(bad) {
			t.Fatal(field)
		}
	}
	update := profiles.ActionRequest{ContractVersion: 1, Operation: "update", ProfileID: created.ProfileID, ExpectedRevision: 1, Configuration: actionConfiguration(*created.Profile)}
	update.Configuration.Name = "later edit"
	if _, e = m.ProfileAction(ctx, update, "admin", "rename"); e != nil {
		t.Fatal(e)
	}
	observed, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || observed.Pending.ProfileState != "changed" || observed.Pending.Receipt.Profile.Name != created.Profile.Name {
		t.Fatal(observed, e)
	}
	// Removing only the isolated own profile simulates a later domain deletion without creating a replacement.
	if _, e = m.db.Exec("DELETE FROM profiles WHERE id=?", created.ProfileID); e != nil {
		t.Fatal(e)
	}
	observed, e = m.OnboardingDraft(ctx, "admin")
	if e != nil || observed.Pending.ProfileState != "missing" || observed.Pending.Receipt.ProfileID != created.ProfileID {
		t.Fatal(observed, e)
	}
}

// An already used conflicting create key must not trap the draft in an unrecoverable pending state.
func TestOnboardingRejectsConflictingCreateKey(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	action := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	if _, e := m.ProfileAction(ctx, action, "admin", "used"); e != nil {
		t.Fatal(e)
	}
	draft := &contracts.OnboardingDraft{Step: 2, Language: "de", UpdateChoice: "manual"}
	if _, e := m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: draft}}, "admin", "draft"); e != nil {
		t.Fatal(e)
	}
	action.Configuration.Name = "another name"
	r := OnboardingRequest{ContractVersion: 1, ExpectedRevision: 1, Change: OnboardingChange{Operation: "prepareProfileCreate", Create: &OnboardingCreate{Key: "used", Request: action}}}
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "prepare"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	out, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || out.Revision != 1 || out.Pending != nil {
		t.Fatal(out, e)
	}
	action.Configuration.Name = in.Profile.Name
	r.Change.Create.Request = action
	if _, e := m.SetOnboardingDraft(ctx, r, "admin", "prepare"); e != nil {
		t.Fatal(e)
	}
	out, e = m.OnboardingDraft(ctx, "admin")
	if e != nil || out.Pending.Status != "committed" {
		t.Fatal(out, e)
	}
}

// A structurally valid corrupt replay cannot attest another draft with the expected revision.
func TestOnboardingRejectsWrongReplayReceipt(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	r := OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: &contracts.OnboardingDraft{Step: 1, Language: "de", UpdateChoice: "manual"}}}
	out, e := m.SetOnboardingDraft(ctx, r, "admin", "save")
	if e != nil {
		t.Fatal(e)
	}
	copyDraft := *out.Draft
	copyDraft.Language = "en"
	out.Draft = &copyDraft
	raw, _ := json.Marshal(out)
	if _, e = m.db.Exec("UPDATE idempotency SET result_json=? WHERE principal='admin' AND operation='onboarding.update' AND key='save'", string(raw)); e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetOnboardingDraft(ctx, r, "admin", "save"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}
