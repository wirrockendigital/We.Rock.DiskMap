package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// Real rollback-journal contention still exercises typed retries without changing production WAL policy.
func TestControlReadsRetryReleasedWriter(t *testing.T) {
	state := t.TempDir()
	writer, err := sql.Open("sqlite", filepath.Join(state, "retry.db")+"?_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err = writer.Exec("CREATE TABLE observations(value); INSERT INTO observations VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", filepath.Join(state, "retry.db")+"?mode=ro&_pragma=busy_timeout(100)")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err = writer.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { time.Sleep(300 * time.Millisecond); _, e := writer.Exec("ROLLBACK"); done <- e }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := retryControlRead(ctx, func() (int, error) {
		var n int
		e := reader.QueryRowContext(ctx, "SELECT value FROM observations").Scan(&n)
		return n, e
	})
	if rollback := <-done; rollback != nil {
		t.Fatal(rollback)
	}
	if err != nil || value != 7 {
		t.Fatal(value, err)
	}
	if _, err = writer.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer writer.Exec("ROLLBACK")
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	_, err = retryControlRead(short, func() (int, error) {
		var n int
		e := reader.QueryRowContext(short, "SELECT value FROM observations").Scan(&n)
		return n, e
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("original deadline lost", err)
	}
}

// Admission retries recheck released leases and do not retain pins or slots while waiting.
func TestHierarchyRetryRevalidatesLease(t *testing.T) {
	m, profile, _ := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	v, e := m.OpenView(context.Background(), "admin", profile, "retry-expiry", "", false)
	if e != nil {
		t.Fatal(e)
	}
	releasePool := holdObservationPool(t, m)
	defer releasePool()
	req := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: v.Lease.ID, Query: query.Request{SnapshotID: *v.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 2, ElementBudget: 50}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.reserveHierarchy(ctx, "admin", req); done <- e }()
	time.Sleep(130 * time.Millisecond)
	// Release uses only the reader lock and must invalidate the next complete attempt.
	if e = m.ReleaseView("admin", profile, v.Lease.ID); e != nil {
		t.Fatal(e)
	}
	releasePool()
	if e = <-done; !errors.Is(e, ErrViewExpired) {
		t.Fatal(e)
	}
	m.readerMu.Lock()
	pins := m.generationReaders[*v.View.SnapshotID]
	m.readerMu.Unlock()
	if pins != 0 || len(m.hierarchySlots) != 0 {
		t.Fatal("reservation leak", pins)
	}
}

// Idle discovery has no durable side effects; rollback leaves its cursor unacknowledged.
func TestRetentionDiscoveryIdleAndRollback(t *testing.T) {
	m, _, job := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	m.retentionWorkMu.Lock()
	defer m.retentionWorkMu.Unlock()
	ctx := context.Background()
	var before, after int
	if e := m.db.QueryRow(`SELECT total_changes()`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		if e := m.discoverRetention(ctx); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.db.QueryRow(`SELECT total_changes()`).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if after != before {
		t.Fatal("idle writes", before, after)
	}
	if _, e := m.db.Exec(`DELETE FROM retention_qualification WHERE job_id=?`, job); e != nil {
		t.Fatal(e)
	}
	m.retentionDiscoveryCursor = 0
	if _, e := m.db.Exec(`CREATE TEMP TRIGGER fail_discovery BEFORE INSERT ON retention_qualification BEGIN SELECT RAISE(ABORT,'test'); END`); e != nil {
		t.Fatal(e)
	}
	if e := m.discoverRetention(ctx); e == nil {
		t.Fatal("failure missing")
	}
	if m.retentionDiscoveryCursor != 0 {
		t.Fatal("failed transaction advanced cursor")
	}
	if _, e := m.db.Exec(`DROP TRIGGER fail_discovery`); e != nil {
		t.Fatal(e)
	}
	if e := m.discoverRetention(ctx); e != nil {
		t.Fatal(e)
	}
	var count int
	if e := m.db.QueryRow(`SELECT count(*) FROM retention_qualification WHERE job_id=?`, job).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

// More pending candidates than delay slots remain fair without unbounded runtime state.
func TestRetentionBackoffFairBeyondCapacity(t *testing.T) {
	m, _, _ := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	m.retentionWorkMu.Lock()
	defer m.retentionWorkMu.Unlock()
	tx, e := m.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 260; i++ {
		if _, e = tx.Exec(`INSERT INTO retention_qualification VALUES(?,?,?,'',0)`, fmt.Sprintf("missing-%03d", i), "profile", "job"); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 520; i++ {
		id, _, _, attempt, e := m.retentionCandidate(context.Background(), now)
		if e != nil || id == "" {
			t.Fatal(i, id, e)
		}
		seen[id] = true
		m.deferRetentionCandidate(id, attempt, now)
		if len(m.retentionRetry) > 128 {
			t.Fatal("unbounded delay map")
		}
		if _, e = m.db.Exec(`UPDATE retention_qualification SET attempts=attempts+1 WHERE snapshot_id=?`, id); e != nil {
			t.Fatal(e)
		}
	}
	if len(seen) != 260 {
		t.Fatal("starved candidates", len(seen))
	}
}

// Shutdown cancels admission during a real observation-capacity wait without leaking a reservation.
func TestHierarchyRetryShutdown(t *testing.T) {
	m, profile, _ := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	v, e := m.OpenView(context.Background(), "admin", profile, "retry-close", "", false)
	if e != nil {
		t.Fatal(e)
	}
	releasePool := holdObservationPool(t, m)
	defer releasePool()
	req := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: v.Lease.ID, Query: query.Request{SnapshotID: *v.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 2, ElementBudget: 50}}
	done := make(chan error, 1)
	go func() { _, e := m.reserveHierarchy(context.Background(), "admin", req); done <- e }()
	time.Sleep(130 * time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case e = <-done:
		if !errors.Is(e, ErrUnavailable) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel retry")
	}
	releasePool()
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
	if len(m.hierarchySlots) != 0 {
		t.Fatal("slot leaked")
	}
}

// A source row that becomes terminal after the cursor passed it is found after wrapping,
// including a fresh runtime initialized from the previously persisted discovery cursor.
func TestRetentionDiscoveryLaterTerminalAndRestart(t *testing.T) {
	m, _, job := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	m.retentionWorkMu.Lock()
	defer m.retentionWorkMu.Unlock()
	if _, e := m.db.Exec(`DELETE FROM retention_qualification WHERE job_id=?; UPDATE jobs SET state='scanning' WHERE id=?`, job, job); e != nil {
		t.Fatal(e)
	}
	m.retentionDiscoveryReady = true
	m.retentionDiscoveryCursor = 0
	if e := m.discoverRetention(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`UPDATE jobs SET state='completed' WHERE id=?`, job); e != nil {
		t.Fatal(e)
	}
	m.retentionDiscoveryReady = false
	for i := 0; i < 3; i++ {
		if e := m.discoverRetention(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	var count int
	if e := m.db.QueryRow(`SELECT count(*) FROM retention_qualification WHERE job_id=?`, job).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

// Missing files remain unqualified without hot attempts, and actual restoration can qualify again.
func TestRetentionBackoffMissingThenRestored(t *testing.T) {
	m, _, job := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	var id string
	if e := m.db.QueryRow(`SELECT json_extract(outcome_json,'$.job.resultSnapshotId') FROM jobs WHERE id=?`, job).Scan(&id); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(m.state, "generations", id, "index.db")
	saved := path + ".held"
	if e := os.Rename(path, saved); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(saved, path)
	if _, e := m.db.Exec(`UPDATE retention_qualification SET stamp='',attempts=10 WHERE snapshot_id=?`, id); e != nil {
		t.Fatal(e)
	}
	m.qualifyRetentionStep(context.Background())
	m.qualifyRetentionStep(context.Background())
	var stamp string
	var attempts int
	if e := m.db.QueryRow(`SELECT stamp,attempts FROM retention_qualification WHERE snapshot_id=?`, id).Scan(&stamp, &attempts); e != nil || stamp != "" || attempts != 11 {
		t.Fatal(stamp, attempts, e)
	}
	if e := os.Rename(saved, path); e != nil {
		t.Fatal(e)
	}
	// Advance only the private scheduling hint; no integrity or file evidence is fabricated.
	m.retentionWorkMu.Lock()
	m.retentionRetry[id] = time.Time{}
	m.retentionWorkMu.Unlock()
	m.qualifyRetentionStep(context.Background())
	if e := m.db.QueryRow(`SELECT stamp FROM retention_qualification WHERE snapshot_id=?`, id).Scan(&stamp); e != nil || stamp == "" {
		t.Fatal(stamp, e)
	}
}

// A snapshot identity changed while admission waits cannot reuse the old request's pin.
func TestHierarchyRetrySnapshotRebinding(t *testing.T) {
	m, profile, _ := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	v, e := m.OpenView(context.Background(), "admin", profile, "retry-rebind", "", false)
	if e != nil {
		t.Fatal(e)
	}
	releasePool := holdObservationPool(t, m)
	defer releasePool()
	req := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: v.Lease.ID, Query: query.Request{SnapshotID: *v.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 2, ElementBudget: 50}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.reserveHierarchy(ctx, "admin", req); done <- e }()
	time.Sleep(130 * time.Millisecond)
	m.readerMu.Lock()
	m.viewLeases[v.Lease.ID].Snapshot = "different-snapshot"
	m.readerMu.Unlock()
	releasePool()
	if e = <-done; !errors.Is(e, query.ErrSnapshot) {
		t.Fatal(e)
	}
	if len(m.hierarchySlots) != 0 {
		t.Fatal("slot leak")
	}
}

// Held observation connections exercise bounded capacity waits without weakening the production journal policy.
func holdObservationPool(t *testing.T, m *Manager) func() {
	t.Helper()
	first, err := m.readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.readDB.Conn(context.Background())
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	var once sync.Once
	return func() { once.Do(func() { first.Close(); second.Close() }) }
}
