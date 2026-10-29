package jobs

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestBrowserReplaySurvivesTerminalRevisionAndRestart never submits a second job for a recorded key.
func TestBrowserReplaySurvivesTerminalRevisionAndRestart(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	identity := BrowserStartIdentity{Principal: "prüfer", Key: "start-001"}
	first, err := m.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil {
		t.Fatal(err)
	}
	if first.Job.Trigger != "ui" {
		t.Fatal(first.Job.Trigger)
	}
	terminal(t, m, first.Job.ID)
	// A profile revision change does not retarget an already accepted operation.
	in.Profile.Revision++
	if err = m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	replay, err := m.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil || !replay.Deduplicated || replay.Job.ID != first.Job.ID || replay.Job.ProfileRevision != 1 {
		t.Fatal(replay, err)
	}
	if _, err = m.StartBrowser("different-profile", "full", identity); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed profile did not conflict", err)
	}
	if _, err = m.StartBrowser(in.Profile.ID, "rebuild", identity); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed mode did not conflict", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(context.Background(), m.state)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	replay, err = restored.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil || replay.Job.ID != first.Job.ID || replay.Job.State != "completed" {
		t.Fatal(replay, err)
	}
	var count int
	if err = restored.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// Identical keys are namespaced by the authenticated principal.
	identity.Principal = "other-admin"
	other, err := restored.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil || other.Job.ID == first.Job.ID {
		t.Fatal(other, err)
	}
	terminal(t, restored, other.Job.ID)
}

// TestBrowserStartConcurrentAndAtomic proves the durable binding precedes any scan execution.
func TestBrowserStartConcurrentAndAtomic(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	identity := BrowserStartIdentity{Principal: "admin", Key: "same-start"}
	const callers = 20
	var wg sync.WaitGroup
	ids := make(chan string, callers)
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := m.StartBrowser(in.Profile.ID, "full", identity)
			if err != nil {
				errs <- err
			} else {
				ids <- out.Job.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var id string
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("duplicate execution", id, next)
		}
		id = next
	}
	if id == "" {
		t.Fatal("no accepted job")
	}
	terminal(t, m, id)
	var before int
	if err := m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&before); err != nil || before != 1 {
		t.Fatal(before, err)
	}
	// A key-store failure rolls back job creation instead of leaving an unbound scan.
	if _, err := m.db.Exec(`CREATE TRIGGER fail_binding BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected binding failure'); END;`); err != nil {
		t.Fatal(err)
	}
	identity.Key = "failing-start"
	if _, err := m.StartBrowser(in.Profile.ID, "full", identity); err == nil {
		t.Fatal("binding failure accepted")
	}
	var after int
	if err := m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&after); err != nil || after != before || m.active != nil {
		t.Fatal("non-atomic job creation", after, err)
	}
}

// TestBrowserIdentityValidation rejects ambiguous key namespaces and preserves ordinary CLI starts.
func TestBrowserIdentityValidation(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []BrowserStartIdentity{{"", "key"}, {"admin", ""}, {"admin\n", "key"}, {" admin", "key"}, {"admin", "bad key"}, {"admin", "bad.key"}, {string([]byte{0xff}), "key"}} {
		if _, err := m.StartBrowser(in.Profile.ID, "full", identity); !errors.Is(err, ErrInvalid) {
			t.Fatal(identity, err)
		}
	}
	out, err := m.Start(in.Profile.ID, "full")
	if err != nil || out.Job.Trigger != "cli" {
		t.Fatal(out, err)
	}
	terminal(t, m, out.Job.ID)
}
