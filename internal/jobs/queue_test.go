package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"sync"
	"testing"
	"time"
)

// queueFixture imports all private test profiles before starting the dispatcher.
func queueFixture(t *testing.T, n int) (*Manager, Import) {
	t.Helper()
	m, in, _ := fixture(t)
	for i := 0; i < n; i++ {
		p := in
		p.Profile.ID = fmt.Sprintf("queue-%02d", i)
		if err := m.ImportProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	return m, in
}

// holdFirstQueue freezes only the first preflight and releases it on failure as well.
func holdFirstQueue(t *testing.T, m *Manager) func() {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var first, closeOnce sync.Once
	m.phaseHook = func(phase string) error {
		if phase == "preflight" {
			first.Do(func() { close(entered); <-release })
		}
		return nil
	}
	unlock := func() { closeOnce.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	if _, err := m.Start("queue-00", "full"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker barrier")
	}
	return unlock
}

// TestQueueBoundedFIFO proves admission, exact replay, rollback on capacity and serial execution.
func TestQueueBoundedFIFO(t *testing.T) {
	m, _ := queueFixture(t, QueueCapacity+2)
	release := holdFirstQueue(t, m)
	ids := []string{}
	for i := 1; i <= QueueCapacity; i++ {
		sub, err := m.StartBrowser(fmt.Sprintf("queue-%02d", i), "full", BrowserStartIdentity{"admin", fmt.Sprintf("key-%d", i)})
		if err != nil || sub.Job.State != "queued" || sub.Job.StartedAt != nil {
			t.Fatal(sub, err)
		}
		ids = append(ids, sub.Job.ID)
	}
	overflow := BrowserStartIdentity{"admin", "overflow"}
	if _, err := m.StartBrowser("queue-17", "full", overflow); !errors.Is(err, ErrQueueFull) {
		t.Fatal(err)
	}
	var n int
	if err := m.db.QueryRow(`SELECT count(*) FROM idempotency WHERE key='overflow'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	joined, err := m.Start("queue-01", "full")
	if err != nil || !joined.Deduplicated || joined.Job.ID != ids[0] {
		t.Fatal(joined, err)
	}
	replay, err := m.StartBrowser("queue-01", "full", BrowserStartIdentity{"admin", "key-1"})
	if err != nil || replay.Job.ID != ids[0] || !replay.Deduplicated {
		t.Fatal(replay, err)
	}
	if _, err = m.Start("queue-01", "rebuild"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = m.StartBrowser("queue-01", "rebuild", BrowserStartIdentity{"admin", "key-1"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	release()
	previousEnd := ""
	for _, id := range ids {
		out := queueTerminal(t, m, id)
		if out.Job.State != "completed" || out.Job.StartedAt == nil || out.Job.EndedAt == nil {
			t.Fatal(out)
		}
		if previousEnd != "" && *out.Job.StartedAt < previousEnd {
			t.Fatal("overlapping FIFO traversal")
		}
		previousEnd = *out.Job.EndedAt
	}
	accepted, err := m.StartBrowser("queue-17", "full", overflow)
	if err != nil || accepted.Deduplicated {
		t.Fatal(accepted, err)
	}
	queueTerminal(t, m, accepted.Job.ID)
}

// TestQueueConcurrentOrigins coalesces UI and CLI requests into one queued captured intent.
func TestQueueConcurrentOrigins(t *testing.T) {
	m, _ := queueFixture(t, 2)
	release := holdFirstQueue(t, m)
	const count = 24
	outputs := make([]Submission, count)
	errs := make([]error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				outputs[i], errs[i] = m.Start("queue-01", "full")
			} else {
				outputs[i], errs[i] = m.StartBrowser("queue-01", "full", BrowserStartIdentity{"admin", fmt.Sprintf("parallel-%d", i)})
			}
		}(i)
	}
	wg.Wait()
	id := outputs[0].Job.ID
	fresh := 0
	for i, o := range outputs {
		if errs[i] != nil || o.Job.ID != id {
			t.Fatal(o, errs[i])
		}
		if !o.Deduplicated {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatal(fresh)
	}
	release()
	terminal(t, m, id)
}

// TestQueueRestartRetainsCapture resumes only waiting work and never replaces its accepted revision.
func TestQueueRestartRetainsCapture(t *testing.T) {
	m, in := queueFixture(t, 3)
	release := holdFirstQueue(t, m)
	first, err := m.Start("queue-00", "full")
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := m.Start("queue-01", "full")
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.Start("queue-02", "full")
	if err != nil {
		t.Fatal(err)
	}
	changed := in.Profile
	changed.ID = "queue-01"
	changed.Name = "updated after acceptance"
	changed.FreshnessSeconds = 300
	if _, err = m.UpdateProfile(context.Background(), changed.ID, 1, changed); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(changed.ID, "full"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	m.cancel()
	release()
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), m.state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	out := terminal(t, reopened, waiting.Job.ID)
	if out.Job.CapturedProfile.Revision != 1 || out.Job.CapturedProfile.Name == changed.Name || out.Job.State != "completed" {
		t.Fatal(out)
	}
	later := terminal(t, reopened, next.Job.ID)
	if *later.Job.StartedAt < *out.Job.EndedAt {
		t.Fatal("restart violated FIFO")
	}
	old, err := reopened.Get(first.Job.ID)
	if err != nil || old.Job.State != "interrupted" {
		t.Fatal(old, err)
	}
	replay, err := reopened.StartBrowser("queue-01", "full", BrowserStartIdentity{"admin", "new-revision"})
	if err != nil || replay.Job.ProfileRevision != 2 {
		t.Fatal(replay, err)
	}
	terminal(t, reopened, replay.Job.ID)
}

// TestQueuedMutationGuards preserves pending captures and rolls back a failed replay insertion.
func TestQueuedMutationGuards(t *testing.T) {
	m, in := queueFixture(t, 3)
	release := holdFirstQueue(t, m)
	queued, err := m.Start("queue-01", "full")
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := m.GetFileTypeMapping(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mapping.Revision++
	if _, err = m.SetFileTypeMapping(context.Background(), MappingUpdate{ContractVersion: 1, ExpectedRevision: mapping.Revision - 1, Mapping: mapping}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	request := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: "queue-01", ExpectedRevision: 1, ConfirmProfileName: in.Profile.Name, ConfirmOwnDataDeletion: true, AcknowledgeTasks: true}
	if _, err = m.ProfileAction(context.Background(), request, "admin", "delete-waiting"); !errors.Is(err, ErrProfileActive) {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`CREATE TRIGGER queue_replay_failure BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartBrowser("queue-02", "full", BrowserStartIdentity{"admin", "failed-acceptance"}); err == nil {
		t.Fatal("accepted without replay")
	}
	var jobs, queue int
	if err = m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if err = m.db.QueryRow(`SELECT count(*) FROM scan_queue`).Scan(&queue); err != nil || jobs != 2 || queue != 1 {
		t.Fatal(jobs, queue, err)
	}
	release()
	terminal(t, m, queued.Job.ID)
}

// Queue integration checks ordering, not host speed; allow serial publication on loaded development hosts.
func queueTerminal(t *testing.T, m *Manager, id string) Outcome {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last Outcome
	for time.Now().Before(deadline) {
		out, err := m.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		last = out
		if terminalJob(out.Job.State) {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("queued job did not finish: %+v", last.Job)
	return Outcome{}
}
