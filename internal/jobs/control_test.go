package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// waitState polls only observed durable state and keeps a finite deadline for failed handshakes.
func waitState(t *testing.T, m *Manager, id, state string) Outcome {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		o, e := m.Get(id)
		if e != nil {
			t.Fatal(e)
		}
		if o.Job.State == state {
			return o
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("state not reached", state)
	return Outcome{}
}

// phaseBarrier exposes one in-flight phase without holding the manager lock.
func phaseBarrier(t *testing.T, m *Manager, wanted string) (<-chan struct{}, func()) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	var one, closed sync.Once
	m.phaseHook = func(phase string) error {
		if phase == wanted {
			one.Do(func() { close(entered); <-release })
		}
		return nil
	}
	unblock := func() { closed.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	return entered, unblock
}

// TestControlPauseResumePhases distinguishes accepted pause from the reached checkpoint in every phase.
func TestControlPauseResumePhases(t *testing.T) {
	for _, phase := range []string{"preflight", "scanning", "validating", "publishing"} {
		t.Run(phase, func(t *testing.T) {
			m, in, _ := fixture(t)
			if e := m.ImportProfile(in); e != nil {
				t.Fatal(e)
			}
			entered, release := phaseBarrier(t, m, phase)
			sub, e := m.Start(in.Profile.ID, "full")
			if e != nil {
				t.Fatal(e)
			}
			<-entered
			request := security.JobActionRequest{ContractVersion: 1, JobID: sub.Job.ID, Action: "pause"}
			receipt, e := m.ControlJob(context.Background(), request, "admin", "pause")
			if e != nil {
				t.Fatal(e)
			}
			pending, e := m.Get(sub.Job.ID)
			if e != nil || pending.Job.State != phase || pending.Control == nil || pending.Control.Requested != "pause" {
				t.Fatal(pending, e)
			}
			if ready, reason := m.Health(context.Background()); !ready {
				t.Fatal(reason)
			}
			release()
			paused := waitState(t, m, sub.Job.ID, "paused")
			if paused.Job.ResultSnapshotID != nil {
				t.Fatal("paused job published")
			}
			request.Action = "resume"
			if _, e = m.ControlJob(context.Background(), request, "admin", "resume"); e != nil {
				t.Fatal(e)
			}
			request.Action = "pause"
			replayed, e := m.ControlJob(context.Background(), request, "admin", "pause")
			if e != nil || replayed != receipt {
				t.Fatal(replayed, e)
			}
			result := terminal(t, m, sub.Job.ID)
			if result.Job.State != "completed" {
				t.Fatal(result)
			}
			request.Action = "cancel"
			if _, e = m.ControlJob(context.Background(), request, "admin", "late-cancel"); !errors.Is(e, ErrConflict) {
				t.Fatal(e)
			}
		})
	}
}

// TestControlAbortPhases leaves the previous complete generation and continues the FIFO worker.
func TestControlAbortPhases(t *testing.T) {
	for _, phase := range []string{"preflight", "scanning", "validating", "publishing"} {
		t.Run(phase, func(t *testing.T) {
			m, in := queueFixture(t, 2)
			baseline, e := m.Start("queue-00", "full")
			if e != nil {
				t.Fatal(e)
			}
			good := terminal(t, m, baseline.Job.ID)
			oldPath := filepath.Join(m.state, "generations", *good.Job.ResultSnapshotID, "index.db")
			before, e := os.ReadFile(oldPath)
			if e != nil {
				t.Fatal(e)
			}
			entered, release := phaseBarrier(t, m, phase)
			sub, e := m.Start("queue-00", "full")
			if e != nil {
				t.Fatal(e)
			}
			<-entered
			queued, e := m.Start("queue-01", "full")
			if e != nil {
				t.Fatal(e)
			}
			request := security.JobActionRequest{ContractVersion: 1, JobID: sub.Job.ID, Action: "cancel"}
			if _, e = m.ControlJob(context.Background(), request, "admin", "abort"); e != nil {
				t.Fatal(e)
			}
			release()
			result := terminal(t, m, sub.Job.ID)
			if result.Job.State != "cancelled" || result.Job.ResultSnapshotID != nil {
				t.Fatal(result)
			}
			after, e := os.ReadFile(oldPath)
			if e != nil || string(before) != string(after) {
				t.Fatal("last-good changed", e)
			}
			if terminal(t, m, queued.Job.ID).Job.State != "completed" {
				t.Fatal("abort stopped FIFO")
			}
			var pointer string
			if e = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id='queue-00'`).Scan(&pointer); e != nil || pointer != *good.Job.ResultSnapshotID {
				t.Fatal(pointer, e)
			}
			entries, e := os.ReadDir(filepath.Join(m.state, "generations"))
			if e != nil {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) == ".staging" {
					t.Fatal("cancelled working data retained")
				}
			}
			_ = in
		})
	}
}

// TestControlQueuedCancelRollbackAndReplay proves queue removal and cancellation share the replay transaction.
func TestControlQueuedCancelRollbackAndReplay(t *testing.T) {
	m, _ := queueFixture(t, 2)
	release := holdFirstQueue(t, m)
	sub, e := m.Start("queue-01", "full")
	if e != nil {
		t.Fatal(e)
	}
	request := security.JobActionRequest{ContractVersion: 1, JobID: sub.Job.ID, Action: "cancel"}
	if _, e = m.db.Exec(`CREATE TRIGGER fail_control BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ControlJob(context.Background(), request, "admin", "cancel"); e == nil {
		t.Fatal("missing replay accepted")
	}
	waitState(t, m, sub.Job.ID, "queued")
	if _, e = m.db.Exec(`DROP TRIGGER fail_control`); e != nil {
		t.Fatal(e)
	}
	first, e := m.ControlJob(context.Background(), request, "admin", "cancel")
	if e != nil {
		t.Fatal(e)
	}
	if terminal(t, m, sub.Job.ID).Job.State != "cancelled" {
		t.Fatal("queued abort failed")
	}
	again, e := m.ControlJob(context.Background(), request, "admin", "cancel")
	if e != nil || first != again {
		t.Fatal(again, e)
	}
	release()
}

// TestControlCommitBoundary rejects late cancellation rather than overwriting a committed result.
func TestControlCommitBoundary(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	m.publishHook = func(phase string) error {
		if phase == "before_commit" {
			once.Do(func() { close(entered); <-release })
		}
		return nil
	}
	sub, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	result := make(chan error, 1)
	go func() {
		_, err := m.ControlJob(context.Background(), security.JobActionRequest{ContractVersion: 1, JobID: sub.Job.ID, Action: "cancel"}, "admin", "late")
		result <- err
	}()
	close(release)
	if e = <-result; !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if terminal(t, m, sub.Job.ID).Job.State != "completed" {
		t.Fatal("late abort changed commit")
	}
}

// TestCancelledWorkRejectsExchangeAndSymlinks never removes a foreign sentinel through a swapped name.
func TestCancelledWorkRejectsExchangeAndSymlinks(t *testing.T) {
	for _, attack := range []string{"directory", "file"} {
		t.Run(attack, func(t *testing.T) {
			parent := t.TempDir()
			work, e := createJobWork(parent, "job.staging", "job")
			if e != nil {
				t.Fatal(e)
			}
			defer work.Close()
			sentinel := filepath.Join(t.TempDir(), "index.db")
			if e = os.WriteFile(sentinel, []byte("foreign"), 0600); e != nil {
				t.Fatal(e)
			}
			target := filepath.Join(parent, "job.staging")
			if attack == "directory" {
				if e = os.Rename(target, target+"-moved"); e != nil {
					t.Fatal(e)
				}
				e = os.Symlink(filepath.Dir(sentinel), target)
			} else {
				e = os.Symlink(sentinel, filepath.Join(target, "index.db"))
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = work.cleanupCancelledGeneration(); e == nil {
				t.Fatal("unsafe replacement accepted")
			}
			data, e := os.ReadFile(sentinel)
			if e != nil || string(data) != "foreign" {
				t.Fatal("foreign sentinel changed", e)
			}
		})
	}
}

// TestControlPauseReplayAfterRestart uses an abrupt child exit with a real reached pause.
func TestControlPauseReplayAfterRestart(t *testing.T) {
	if base := os.Getenv("DISKMAP_CONTROL_CHILD"); base != "" {
		m, e := Open(context.Background(), filepath.Join(base, "state"))
		if e != nil {
			t.Fatal(e)
		}
		entered, release := phaseBarrier(t, m, "scanning")
		sub, e := m.Start("probe", "full")
		if e != nil {
			t.Fatal(e)
		}
		<-entered
		_, e = m.ControlJob(context.Background(), security.JobActionRequest{ContractVersion: 1, JobID: sub.Job.ID, Action: "pause"}, "admin", "durable-pause")
		if e != nil {
			t.Fatal(e)
		}
		release()
		waitState(t, m, sub.Job.ID, "paused")
		if e = os.WriteFile(filepath.Join(base, "paused-job"), []byte(sub.Job.ID), 0600); e != nil {
			t.Fatal(e)
		}
		os.Exit(87)
	}
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	baseline, e := m.Start("probe", "full")
	if e != nil {
		t.Fatal(e)
	}
	good := terminal(t, m, baseline.Job.ID)
	old := filepath.Join(m.state, "generations", *good.Job.ResultSnapshotID, "index.db")
	before, e := os.ReadFile(old)
	if e != nil {
		t.Fatal(e)
	}
	base := filepath.Dir(m.state)
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestControlPauseReplayAfterRestart$", "-test.timeout=20s")
	child.Env = append(os.Environ(), "DISKMAP_CONTROL_CHILD="+base)
	output, e := child.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 87 {
		t.Fatal(e, string(output))
	}
	id, e := os.ReadFile(filepath.Join(base, "paused-job"))
	if e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(context.Background(), m.state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	interrupted, e := reopened.Get(string(id))
	if e != nil || interrupted.Job.State != "interrupted" {
		t.Fatal(interrupted, e)
	}
	request := security.JobActionRequest{ContractVersion: 1, JobID: string(id), Action: "pause"}
	if _, e = reopened.ControlJob(context.Background(), request, "admin", "durable-pause"); e != nil {
		t.Fatal(e)
	}
	request.Action = "resume"
	if _, e = reopened.ControlJob(context.Background(), request, "admin", "new-resume"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	after, e := os.ReadFile(old)
	if e != nil || string(before) != string(after) {
		t.Fatal("restart changed last-good", e)
	}
}

// TestQueueRemovalNeverCancelsActive proves stale queue clicks cannot interrupt the worker.
func TestQueueRemovalNeverCancelsActive(t *testing.T) {
	m, _ := queueFixture(t, 2)
	release := holdFirstQueue(t, m)
	var activeID string
	if err := m.db.QueryRow(`SELECT id FROM jobs WHERE state != 'queued'`).Scan(&activeID); err != nil {
		t.Fatal(err)
	}
	request := security.JobActionRequest{ContractVersion: 1, JobID: activeID, Action: "cancel-queued"}
	if _, err := m.ControlJob(context.Background(), request, "admin", "active-guard"); !errors.Is(err, ErrConflict) {
		t.Fatalf("active removal: %v", err)
	}
	sub, err := m.Start("queue-01", "full")
	if err != nil {
		t.Fatal(err)
	}
	request.JobID = sub.Job.ID
	first, err := m.ControlJob(context.Background(), request, "admin", "queued-remove")
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.ControlJob(context.Background(), request, "admin", "queued-remove")
	if err != nil || first != again {
		t.Fatal(again, err)
	}
	if terminal(t, m, sub.Job.ID).Job.State != "cancelled" {
		t.Fatal("queued job not cancelled")
	}
	release()
	if terminal(t, m, activeID).Job.State != "completed" {
		t.Fatal("active scan was affected")
	}
}
