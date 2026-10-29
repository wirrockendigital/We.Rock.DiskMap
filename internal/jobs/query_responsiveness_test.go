package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// A real waiting retention SQL operation cannot monopolize query admission or observations.
func TestRetentionDiscoveryWriterPoolIsolation(t *testing.T) {
	m, profile, job := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	view, e := m.OpenView(context.Background(), "admin", profile, "discovery-view", "", false)
	if e != nil {
		t.Fatal(e)
	}
	// Force a real newly discovered candidate rather than relying on obsolete idle writes.
	if _, e = m.db.Exec(`DELETE FROM retention_qualification WHERE job_id=?`, job); e != nil {
		t.Fatal(e)
	}
	m.retentionWorkMu.Lock()
	m.retentionDiscoveryReady = true
	m.retentionDiscoveryCursor = 0
	m.retentionWorkMu.Unlock()
	conn, e := m.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	before := m.db.Stats().WaitCount
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.qualifyRetentionStep(ctx); close(done) }()
	defer func() { cancel(); conn.Close(); <-done }()
	until := time.Now().Add(time.Second)
	for m.db.Stats().WaitCount == before && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if m.db.Stats().WaitCount == before {
		t.Fatal("retention did not reach held writer pool")
	}
	request := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *view.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 2, ElementBudget: 50}}
	readctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, e = m.ReadHierarchy(readctx, "admin", request, false); e != nil {
		t.Fatal("hierarchy blocked", e)
	}
	if _, e = m.ObserveJob(readctx, job); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Status(readctx); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Progress(readctx, job); e != nil {
		t.Fatal(e)
	}
	if _, e = m.History(readctx, profile); e != nil {
		t.Fatal(e)
	}
}

// Pool mode, cardinality and SQLite journal semantics remain explicit and testable.
func TestControlReadPoolPolicyAndExclusiveWriter(t *testing.T) {
	m, profile, job := observedFixture(t)
	view, err := m.OpenView(context.Background(), "admin", profile, "exclusive-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	request := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *view.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 2, ElementBudget: 50}}
	if m.readDB.Stats().MaxOpenConnections != 2 {
		t.Fatal(m.readDB.Stats())
	}
	var journal string
	if e := m.readDB.QueryRow("PRAGMA journal_mode").Scan(&journal); e != nil || journal != "wal" {
		t.Fatal(journal, e)
	}
	if _, e := m.readDB.Exec("DELETE FROM jobs"); e == nil {
		t.Fatal("read pool accepted write")
	}
	// Qualify the generation before timing the writer-isolation check; cold file
	// validation is independent of SQLite writer blocking and can exceed 100 ms.
	warmCtx, warmCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, warmErr := m.ReadHierarchy(warmCtx, "admin", request, false)
	warmCancel()
	if warmErr != nil {
		t.Fatal("cold hierarchy qualification failed", warmErr)
	}
	conn, e := m.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if _, e = conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); e != nil {
		t.Fatal(e)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	for _, tc := range []struct {
		name string
		read func(context.Context) error
	}{
		{"hierarchy", func(c context.Context) error { _, e := m.ReadHierarchy(c, "admin", request, false); return e }},
		{"status", func(c context.Context) error { _, e := m.Status(c); return e }},
		{"progress", func(c context.Context) error { _, e := m.Progress(c, job); return e }},
		{"history", func(c context.Context) error { _, e := m.History(c, profile); return e }},
		{"job", func(c context.Context) error { _, e := m.ObserveJob(c, job); return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			err := tc.read(ctx)
			if err != nil {
				t.Fatal("WAL reader blocked by writer", err)
			}
		})
	}

}

// Failed opens release the pre-open reservation, and a read pool cannot outlive manager shutdown.
func TestQueryPoolCloseAndFailedPin(t *testing.T) {
	m, _, job := observedFixture(t)
	if _, _, e := m.generationDB("absent-generation"); e == nil {
		t.Fatal("missing generation opened")
	}
	m.readerMu.Lock()
	n := m.generationReaders["absent-generation"]
	m.readerMu.Unlock()
	if n != 0 {
		t.Fatal("failed open leaked pin", n)
	}
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := m.ObserveJob(context.Background(), job); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e := m.Status(context.Background()); e != ErrUnavailable {
		t.Fatal(e)
	}
	if e := m.readDB.Ping(); e == nil {
		t.Fatal("read pool remained open")
	}
}

// A reader pool observes committed state only; reserved writer changes never leak into a receipt.
func TestControlReadPoolCommittedObservation(t *testing.T) {
	m, _, job := observedFixture(t)
	tx, err := m.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE jobs SET state='scanning',outcome_json=json_set(outcome_json,'$.job.state','scanning') WHERE id=?`, job); err != nil {
		t.Fatal(err)
	}
	observed, err := m.ObserveJob(context.Background(), job)
	if err != nil || observed.Job.State != "completed" {
		t.Fatal(observed.Job.State, err)
	}
	status, err := m.Status(context.Background())
	if err != nil || status.Active != nil || len(status.Queued) != 0 {
		t.Fatal(status, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

// Short shared locks must not make writer commits fail; cancellation remains bounded
// even while SQLite's native busy handler waits for an exclusive lock to disappear.
func TestControlBusyWaitBoundedAndWriterProgress(t *testing.T) {
	m, _, job := observedFixture(t)
	for _, db := range []*sql.DB{m.db, m.readDB} {
		var timeout int
		if err := db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 100 {
			t.Fatal(timeout, err)
		}
	}
	tx, err := m.readDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM jobs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, e := m.db.Exec("UPDATE jobs SET state=state WHERE id=?", job); done <- e }()
	// Keep a genuine shared file lock long enough for the writer's busy handler.
	time.Sleep(30 * time.Millisecond)
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal("short reader lock rejected writer", err)
	}
	releasePool := holdObservationPool(t, m)
	defer releasePool()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = m.ObserveJob(ctx, job)
	elapsed := time.Since(start)
	if err != ErrUnavailable || ctx.Err() == nil {
		t.Fatal(err, ctx.Err())
	}
	// Native busy sleeps are cooperative, not a hard context wall-clock guarantee.
	if elapsed > 300*time.Millisecond {
		t.Fatal("busy wait exceeded bounded cancellation allowance", elapsed)
	}
	t.Logf("cancelled observation after %s with 100ms native busy bound", elapsed)
}

// An idle integrity worker must discover its empty queue without the writer
// connection or lifecycle ownership, including when retention is waiting too.
func TestIntegrityDiscoveryWriterPoolIsolation(t *testing.T) {
	m, _, _ := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { m.integrityStep(ctx); close(done) }()
	defer func() { cancel(); conn.Close(); <-done }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle integrity discovery waited for held writer connection")
	}
	// Completion proves this worker did not wait on the held writer. A global
	// TryLock here cannot identify this owner: unrelated live workers may acquire it.
}

// These maintenance tests require an idle dispatcher before deliberately
// saturating its writer pool; terminal publication precedes the queue handoff.
func waitQueryFixtureDispatch(t *testing.T, m *Manager) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		m.mu.Lock()
		running := m.workerRunning
		m.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("fixture dispatcher did not finish its terminal handoff")
}
