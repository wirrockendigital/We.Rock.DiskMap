package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Waiting remains independent of long maintenance and missing/corrupt generation files.
func TestWaitStatusBypassesLifecycleAndIndex(t *testing.T) {
	m := diagnosticFixture(t)
	snapshot := "nonexistent-index"
	expected := Outcome{Job: contracts.Job{BytesObserved: "0", ID: "job", ProfileID: "profile", ScanMode: "full", State: "completed", ResultSnapshotID: &snapshot}, SnapshotState: "complete"}
	raw, _ := json.Marshal(expected)
	if _, e := m.db.Exec(`INSERT INTO jobs VALUES('job','completed',?)`, string(raw)); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := time.Now()
	out, e := m.WaitStatus(ctx, "job")
	if e != nil || out.Job.State != "completed" || out.Job.ResultSnapshotID == nil || *out.Job.ResultSnapshotID != snapshot || out.StoredData != nil || time.Since(started) > time.Second {
		t.Fatal(out, e)
	}
}

// Polling observes committed transitions and fails safely on identity corruption or cancellation.
func TestWaitStatusTransitionsAndFailure(t *testing.T) {
	m := diagnosticFixture(t)
	for _, state := range []string{"queued", "scanning", "paused", "scanning", "completed"} {
		out := Outcome{Job: contracts.Job{BytesObserved: "0", ID: "job", ProfileID: "profile", ScanMode: "full", State: state}}
		raw, _ := json.Marshal(out)
		m.db.Exec(`INSERT OR REPLACE INTO jobs VALUES('job',?,?)`, state, string(raw))
		got, e := m.WaitStatus(context.Background(), "job")
		if e != nil || got.Job.State != state {
			t.Fatal(got, e)
		}
	}
	m.db.Exec(`UPDATE jobs SET state='paused'`)
	if _, e := m.WaitStatus(context.Background(), "job"); e != ErrUnavailable {
		t.Fatal(e)
	}
	for _, id := range []string{"../bad", "missing"} {
		if _, e := m.WaitStatus(context.Background(), id); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.WaitStatus(ctx, "job"); e != ErrUnavailable {
		t.Fatal(e)
	}
	m.db.Close()
	if _, e := m.WaitStatus(context.Background(), "job"); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// A saturated SQL pool obeys the caller's deadline without acquiring the lifecycle lock.
func TestWaitStatusSQLDeadline(t *testing.T) {
	m := diagnosticFixture(t)
	held, e := m.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	at := time.Now()
	_, e = m.WaitStatus(ctx, "job")
	if e != ErrUnavailable || time.Since(at) > time.Second {
		t.Fatal(e, time.Since(at))
	}
}
