package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// Graceful service shutdown must not masquerade as an explicit user cancellation.
func TestRecoveryServiceStopVersusUserCancel(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprint(manual), func(t *testing.T) {
			m, in, _ := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			first, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			good := terminal(t, m, first.Job.ID)
			old := *good.Job.ResultSnapshotID
			file := filepath.Join(m.state, "generations", old, "index.db")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			m.phaseHook = func(phase string) error {
				if phase == "scanning" {
					close(entered)
					<-m.ctx.Done()
					return m.ctx.Err()
				}
				return nil
			}
			next, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			if manual {
				_, err = m.ControlJob(context.Background(), security.JobActionRequest{ContractVersion: 1, JobID: next.Job.ID, Action: "cancel"}, "admin", "manual-before-stop")
				if err != nil {
					t.Fatal(err)
				}
			}
			state := m.state
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := Open(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			got, err := restored.Get(next.Job.ID)
			want := "interrupted"
			if manual {
				want = "cancelled"
			}
			if err != nil || got.Job.State != want || got.Job.EndedAt == nil || got.Job.ResultSnapshotID != nil {
				t.Fatal(got, err)
			}
			var count int
			if err = restored.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count); err != nil || count != 2 {
				t.Fatal("restart synthesized work", count, err)
			}
			var active string
			if err = restored.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active); err != nil || active != old {
				t.Fatal(active, err)
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("last good changed", err)
			}
		})
	}
}

// Recovery validates SQL/JSON binding before committing any page, including earlier valid pages.
func TestRecoveryBindingFailureRollsBackAllPages(t *testing.T) {
	m, _, _ := fixture(t)
	for i := 0; i < 66; i++ {
		id := fmt.Sprintf("recover-%03d", i)
		o := Outcome{Job: contracts.Job{ID: id, ProfileID: "probe", State: "scanning", BytesObserved: "0"}}
		raw, _ := json.Marshal(o)
		if i == 65 {
			o.Job.ID = "recover-000"
			raw, _ = json.Marshal(o)
		}
		if _, err := m.db.Exec(`INSERT INTO jobs VALUES(?,'scanning',?)`, id, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.recoverInterruptedJobs(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var remaining, history int
	if err := m.db.QueryRow(`SELECT count(*) FROM jobs WHERE state='scanning'`).Scan(&remaining); err != nil || remaining != 66 {
		t.Fatal(remaining, err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM job_phases`).Scan(&history); err != nil || history != 0 {
		t.Fatal(history, err)
	}
	// Repair only the synthetic corrupt record, then prove all pages finish once and do not resume work.
	o := Outcome{Job: contracts.Job{ID: "recover-065", ProfileID: "probe", State: "scanning", BytesObserved: "0"}}
	raw, _ := json.Marshal(o)
	if _, err := m.db.Exec(`UPDATE jobs SET outcome_json=? WHERE id='recover-065'`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := m.recoverInterruptedJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.recoverInterruptedJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM jobs WHERE state='interrupted'`).Scan(&remaining); err != nil || remaining != 66 {
		t.Fatal(remaining, err)
	}
	if err := m.db.QueryRow(`SELECT count(*) FROM job_phases WHERE state='interrupted'`).Scan(&history); err != nil || history != 66 {
		t.Fatal(history, err)
	}
}

// Unknown, unbound crash work stays isolated; neither a lock pathname nor a symlink authorizes deletion.
func TestRecoveryPreservesUnboundWorkAndReacquiresReleasedLock(t *testing.T) {
	m, _, source := fixture(t)
	state := m.state
	if other, err := Open(context.Background(), state); !errors.Is(err, ErrConflict) {
		if other != nil {
			other.Close()
		}
		t.Fatal("live lock bypassed", err)
	}
	work := filepath.Join(state, "generations", "unbound.staging")
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("unbound partial SQLite bytes")
	if err := os.WriteFile(filepath.Join(work, "index.db"), sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(state, "generations", "foreign.staging")); err != nil {
		t.Fatal(err)
	}
	o := Outcome{Job: contracts.Job{ID: "crashed", ProfileID: "probe", State: "scanning", BytesObserved: "0"}}
	if err := m.save(o); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.Get("crashed")
	if err != nil || got.Job.State != "interrupted" || len(got.Job.Warnings) != 1 || got.Job.Warnings[0].Code != "WORK_RECOVERY_REVIEW" {
		t.Fatal(got, err)
	}
	after, err := os.ReadFile(filepath.Join(work, "index.db"))
	if err != nil || !bytes.Equal(after, sentinel) {
		t.Fatal("unqualified work changed", err)
	}
	original, err := os.ReadFile(filepath.Join(source, "data"))
	if err != nil || string(original) != "abc" {
		t.Fatal("source changed", err)
	}
	if _, err = os.Readlink(filepath.Join(state, "generations", "foreign.staging")); err != nil {
		t.Fatal("unqualified symlink changed", err)
	}
}
