package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// Preparation uses the same public contracts as the Wizard, without issuing a create.
func prepareRejectedCreate(t *testing.T, m *Manager, in Import) profiles.ActionRequest {
	t.Helper()
	ctx := context.Background()
	_, e := m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, Change: OnboardingChange{Operation: "replace", Draft: &contracts.OnboardingDraft{Step: 4, Language: "de", UpdateChoice: "manual"}}}, "admin", "draft")
	if e != nil {
		t.Fatal(e)
	}
	action := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	_, e = m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, ExpectedRevision: 1, Change: OnboardingChange{Operation: "prepareProfileCreate", Create: &OnboardingCreate{Key: "create-intent", Request: action}}}, "admin", "prepare")
	if e != nil {
		t.Fatal(e)
	}
	return action
}

// A lost rejection response remains final even after the source returns and the draft is released.
func TestOnboardingRejectedCreateRemainsFinal(t *testing.T) {
	m, in, _, catalog := nativeFixture(t, "volumes")
	ctx := context.Background()
	action := prepareRejectedCreate(t, m, in)
	saved := detachedCatalog(*catalog)
	catalog.Volumes = nil
	if _, e := m.ProfileAction(ctx, action, "admin", "create-intent"); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	got, e := m.OnboardingDraft(ctx, "admin")
	if e != nil || got.Pending == nil || got.Pending.Status != "rejected" || got.Pending.RejectionCode != "INVALID_REQUEST" || !ValidOnboardingResponse(got) {
		t.Fatalf("%+v %v", got, e)
	}
	// Reopening proves the rejected result is durable, not merely an in-memory lock decision.
	state := m.state
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	m, e = Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(saved), nil }
	ack := OnboardingRequest{ContractVersion: 1, ExpectedRevision: 2, Change: OnboardingChange{Operation: "acknowledgeProfileRejection"}}
	// Both orderings are safe: create replay cannot become successful while acknowledgment clears pending.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := m.ProfileAction(ctx, action, "admin", "create-intent"); !errors.Is(err, ErrInvalid) {
			t.Errorf("retry: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := m.SetOnboardingDraft(ctx, ack, "admin", "ack"); err != nil {
			t.Errorf("ack: %v", err)
		}
	}()
	wg.Wait()
	if _, e = m.SetOnboardingDraft(ctx, ack, "admin", "ack"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ProfileAction(ctx, action, "admin", "create-intent"); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	var count int
	if e = m.db.QueryRow("SELECT count(*) FROM profiles").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	got, e = m.OnboardingDraft(ctx, "admin")
	if e != nil || got.Pending != nil || got.Draft == nil || got.Draft.ProfileID != nil {
		t.Fatal(got, e)
	}
}

// Neither missing receipts, temporary failures nor successful creates authorize discarding an intent.
func TestOnboardingRejectAckRequiresDurableRejection(t *testing.T) {
	for _, mode := range []string{"unresolved", "unavailable", "committed"} {
		t.Run(mode, func(t *testing.T) {
			m, in, _, _ := nativeFixture(t, "volumes")
			action := prepareRejectedCreate(t, m, in)
			ctx := context.Background()
			if mode == "unavailable" {
				m.catalog = func(context.Context, profileSources) (profileSources, error) { return profileSources{}, ErrUnavailable }
				if _, e := m.ProfileAction(ctx, action, "admin", "create-intent"); !errors.Is(e, ErrUnavailable) {
					t.Fatal(e)
				}
			}
			if mode == "committed" {
				if _, e := m.ProfileAction(ctx, action, "admin", "create-intent"); e != nil {
					t.Fatal(e)
				}
			}
			_, e := m.SetOnboardingDraft(ctx, OnboardingRequest{ContractVersion: 1, ExpectedRevision: 2, Change: OnboardingChange{Operation: "acknowledgeProfileRejection"}}, "admin", "ack")
			if !errors.Is(e, ErrOnboardingPending) {
				t.Fatal(e)
			}
		})
	}
}

// A corrupt rejection cannot masquerade as either a successful create or an actionable rejection.
func TestOnboardingRejectReceiptStrict(t *testing.T) {
	for _, r := range []ProfileActionResult{{Operation: "create", RejectionCode: "OTHER"}, {Operation: "update", RejectionCode: "INVALID_REQUEST"}, {Operation: "create", RejectionCode: "INVALID_REQUEST", ProfileID: "foreign"}, {Operation: "create", RejectionCode: "INVALID_REQUEST", CleanupScheduled: true}} {
		if validOnboardingRejection(r) {
			t.Fatal(r)
		}
	}
}

// A partial domain write is rolled back before the rejection replay is committed.
func TestOnboardingRejectionRollsBackSavepoint(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	action := prepareRejectedCreate(t, m, in)
	ctx := context.Background()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, e := m.db.Exec("CREATE TABLE rejection_domain_probe(value TEXT)"); e != nil {
		t.Fatal(e)
	}
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec("SAVEPOINT onboarding_create"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("INSERT INTO rejection_domain_probe VALUES('partial')"); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(action)
	e = persistOnboardingRejection(ctx, tx, ReplayRequest{"admin", "profile.create", "create-intent", string(raw)}, ErrInvalid)
	if !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	var count int
	if e = m.db.QueryRow("SELECT count(*) FROM rejection_domain_probe").Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	if e = m.db.QueryRow("SELECT count(*) FROM idempotency WHERE principal='admin' AND operation='profile.create' AND key='create-intent'").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}
