package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// diagnosticFixture has actual SQLite metadata but no worker or source catalog to obscure observations.
func diagnosticFixture(t *testing.T) *Manager {
	t.Helper()
	db, e := sql.Open("sqlite", ":memory:")
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{`CREATE TABLE jobs(id TEXT PRIMARY KEY,state TEXT,outcome_json TEXT)`, `CREATE TABLE job_progress(job_id TEXT PRIMARY KEY,detail_json TEXT)`, `CREATE TABLE scan_queue(sequence INTEGER PRIMARY KEY)`, `CREATE TABLE integrity_generations(quality TEXT)`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	return &Manager{db: db, readDB: db, ctx: context.Background(), state: "/private/never-export", spaceProbe: func() (uint64, error) { return IndexReserveBytes * 2, nil }}
}

// Reachability is not integrity, and private identifiers or failures never leave this DTO.
func TestDiagnosticsActualStatesAndPrivacy(t *testing.T) {
	m := diagnosticFixture(t)
	started := time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)
	if _, e := m.db.Exec(`INSERT INTO jobs VALUES('private-id','paused',?)`, fmt.Sprintf(`{"job":{"startedAt":%q,"warnings":["/secret/path"]}}`, started)); e != nil {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`INSERT INTO job_progress VALUES('private-id','{"observed":true,"sample":{"warnings":9007199254740993}}')`); e != nil {
		t.Fatal(e)
	}
	m.active = &Outcome{}
	m.active.Job.ID = "private-id"
	out, e := m.DiagnosticStatus(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if out.Phase != "paused" || out.ElapsedMillis == nil || *out.ElapsedMillis < 1900 || out.Warnings == nil || *out.Warnings != 9007199254740994 || out.ControlDatabase.Integrity != "unknown" || out.Queue.Waiting == nil || *out.Queue.Waiting != 0 {
		t.Fatalf("bad observation: %+v", out)
	}
	raw, _ := json.Marshal(out)
	for _, secret := range []string{"private-id", "/secret", "/private"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("private value escaped")
		}
	}
	m.spaceProbe = func() (uint64, error) { return 0, errors.New("/secret/private failure") }
	out, e = m.DiagnosticStatus(context.Background())
	if e != nil || out.Storage.State != "unavailable" || out.Storage.FreeBytes != nil || out.Capabilities.ScanAvailable != nil {
		t.Fatal(out, e)
	}
	m.spaceProbe = func() (uint64, error) { return 1, nil }
	out, _ = m.DiagnosticStatus(context.Background())
	if out.Storage.State != "low" || out.Capabilities.ScanAvailable == nil || *out.Capabilities.ScanAvailable {
		t.Fatal(out)
	}
}

// Maintenance contention returns promptly rather than consuming a request's whole budget.
func TestDiagnosticsBusyClosedAndUnavailable(t *testing.T) {
	m := diagnosticFixture(t)
	m.mu.Lock()
	at := time.Now()
	out, e := m.DiagnosticStatus(context.Background())
	m.mu.Unlock()
	if e != nil || out.Backend != "busy" || out.Phase != "unknown" || time.Since(at) > 100*time.Millisecond {
		t.Fatal(out, e)
	}
	m.closed = true
	out, _ = m.DiagnosticStatus(context.Background())
	if out.Backend != "stopping" || out.Capabilities.ScanAvailable == nil || *out.Capabilities.ScanAvailable {
		t.Fatal(out)
	}
	m.closed = false
	m.db.Close()
	out, _ = m.DiagnosticStatus(context.Background())
	if out.ControlDatabase.Availability != "unavailable" || out.ControlDatabase.Integrity != "unknown" || out.Phase != "unknown" {
		t.Fatal(out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.DiagnosticStatus(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

// Bounded observations do not imply that an unexamined remainder is healthy.
func TestDiagnosticsSampleCapAndUnknownPhase(t *testing.T) {
	m := diagnosticFixture(t)
	for i := 0; i < 140; i++ {
		if _, e := m.db.Exec(`INSERT INTO integrity_generations VALUES('corrupt')`); e != nil {
			t.Fatal(e)
		}
	}
	out, e := m.DiagnosticStatus(context.Background())
	if e != nil || out.Generations.Sampled != 128 || out.Generations.Corrupt != 128 || !out.Generations.Truncated || out.Generations.Observation != "recorded" {
		t.Fatal(out, e)
	}
	m.db.Exec(`INSERT INTO jobs VALUES('id','/private/unknown','{}')`)
	m.active = &Outcome{}
	m.active.Job.ID = "id"
	out, _ = m.DiagnosticStatus(context.Background())
	if out.Phase != "unknown" || out.Warnings != nil {
		t.Fatal(out)
	}
	m.db.Exec(`UPDATE integrity_generations SET quality='unrecognized' WHERE rowid=1`)
	out, _ = m.DiagnosticStatus(context.Background())
	if out.Generations.Observation != "unavailable" || out.Generations.Sampled != 0 {
		t.Fatal(out)
	}
}
