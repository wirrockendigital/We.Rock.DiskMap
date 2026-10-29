package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"sync"
	"testing"
)

// TestGenericReplayActions proves transactional security semantics, not feature state machines.
func TestGenericReplayActions(t *testing.T) {
	m, _, _ := fixture(t)
	if _, err := m.db.Exec(`CREATE TABLE action_probe(operation TEXT PRIMARY KEY,value INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"job.pause", "job.resume", "job.cancel", "profile.create", "profile.update", "profile.duplicate", "profile.deactivate", "profile.delete"} {
		request := ReplayRequest{Principal: "admin", Operation: operation, Key: "intent-1", Payload: `{"contractVersion":1,"id":"opaque"}`}
		apply := func(r ReplayRequest) (json.RawMessage, error) {
			m.mu.Lock()
			defer m.mu.Unlock()
			tx, err := m.db.Begin()
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			result, found, err := LookupReplay(tx, r)
			if err != nil || found {
				return result, err
			}
			if _, err = tx.Exec(`INSERT INTO action_probe VALUES(?,1)`, r.Operation); err != nil {
				return nil, err
			}
			result = json.RawMessage(`{"id":"generated-identity","revision":1}`)
			if err = RecordReplay(tx, r, result); err != nil {
				return nil, err
			}
			err = CommitReplay(tx, func() {
				var n int
				if m.db.QueryRow(`SELECT count(*) FROM action_probe WHERE operation=?`, r.Operation).Scan(&n) != nil || n != 1 {
					t.Error("effect preceded commit")
				}
			})
			return result, err
		}
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := apply(request)
				if err != nil || string(result) != `{"id":"generated-identity","revision":1}` {
					t.Error(string(result), err)
				}
			}()
		}
		wg.Wait()
		request.Payload = `{"contractVersion":1,"id":"different"}`
		if _, err := apply(request); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal(err)
		}
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), m.state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	tx, err := reopened.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, found, err := LookupReplay(tx, ReplayRequest{"admin", "profile.create", "intent-1", `{"contractVersion":1,"id":"opaque"}`})
	if err != nil || !found || string(result) != `{"id":"generated-identity","revision":1}` {
		t.Fatal(string(result), found, err)
	}
}

// TestReplaySchemaMigration preserves existing scan identities and rolls back partial DDL on failure.
func TestReplaySchemaMigration(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	identity := BrowserStartIdentity{"admin", "old-key"}
	run, err := m.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	// Reconstruct the exact previous private schema with its already accepted native scan key.
	if _, err = m.db.Exec(`UPDATE idempotency SET result_json=json_extract(result_json,'$.jobId'); ALTER TABLE idempotency RENAME COLUMN result_json TO job_id; PRAGMA user_version=0; DROP TABLE schema_steps; DROP TABLE onboarding_drafts; DROP TABLE exclusion_template; CREATE TRIGGER fail_migration BEFORE UPDATE ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	if err = migrateReplaySchema(m.db); err == nil {
		t.Fatal("partial migration accepted")
	}
	var preserved string
	if err = m.db.QueryRow(`SELECT job_id FROM idempotency WHERE key=?`, identity.Key).Scan(&preserved); err != nil || preserved != run.Job.ID {
		t.Fatal(preserved, err)
	}
	var version int
	if err = m.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 0 {
		t.Fatal(version, err)
	}
	if _, err = m.db.Exec(`DROP TRIGGER fail_migration`); err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	// Schema evolution now requires the explicit offline backup/candidate publication boundary.
	for _, mode := range []string{"prepare", "apply"} {
		if err = Upgrade(context.Background(), m.state, mode); err != nil {
			t.Fatal(mode, err)
		}
	}
	reopened, err := Open(context.Background(), m.state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replay, err := reopened.StartBrowser(in.Profile.ID, "full", identity)
	if err != nil || !replay.Deduplicated || replay.Job.ID != run.Job.ID {
		t.Fatal(replay, err)
	}
	if err = reopened.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != migration.ControlVersion {
		t.Fatal(version, err)
	}
	// The replay migration now passes through both explicit baseline and personal-preference steps.
	var baseline, personal string
	if reopened.db.QueryRow(`SELECT name FROM schema_steps WHERE version=3`).Scan(&baseline) != nil || baseline != "control-baseline-2026-10" || reopened.db.QueryRow(`SELECT name FROM schema_steps WHERE version=4`).Scan(&personal) != nil || personal != "personal-preferences-v1" {
		t.Fatal(baseline, personal)
	}
	preference, err := reopened.Preferences(context.Background(), identity.Principal)
	if err != nil || preference.Revision != 0 || preference.Version != contracts.PreferenceVersion {
		t.Fatal(preference, err)
	}
	var personalRows int
	if reopened.db.QueryRow(`SELECT count(*) FROM user_preferences`).Scan(&personalRows) != nil || personalRows != 0 {
		t.Fatal("migration/default read inserted personal rows", personalRows)
	}
	var profiles, jobs int
	if reopened.db.QueryRow(`SELECT count(*) FROM profiles`).Scan(&profiles) != nil || reopened.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs) != nil || profiles != 1 || jobs != 1 {
		t.Fatal(profiles, jobs)
	}
}

// TestGenericReplayRollback prevents a domain mutation or post-commit effect without replay storage.
func TestGenericReplayRollback(t *testing.T) {
	m, _, _ := fixture(t)
	if _, err := m.db.Exec(`CREATE TABLE action_probe(value INTEGER); CREATE TRIGGER fail_action BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END;`); err != nil {
		t.Fatal(err)
	}
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO action_probe VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	if err = RecordReplay(tx, ReplayRequest{"admin", "profile.delete", "key", `{"id":"p"}`}, json.RawMessage(`{"deleted":true}`)); err == nil {
		t.Fatal("binding accepted")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	effects := 0
	if CommitReplay(tx, func() { effects++ }) == nil || effects != 0 {
		t.Fatal("effect after failed transaction")
	}
	var count int
	if m.db.QueryRow(`SELECT count(*) FROM action_probe`).Scan(&count) != nil || count != 0 {
		t.Fatal(count)
	}
}
