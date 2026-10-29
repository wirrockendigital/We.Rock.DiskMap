package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// TestJobPhaseTransitions rejects skipped and repeated transitions and terminal rewrites.
func TestJobPhaseTransitions(t *testing.T) {
	chain := []string{"queued", "preflight", "scanning", "validating", "publishing", "completed"}
	for i, a := range chain {
		for j, b := range chain {
			want := j == i+1
			if got := validPhaseTransition(a, b); got != want {
				t.Fatal(a, b, got, want)
			}
		}
	}
	for _, a := range chain[:len(chain)-1] {
		if !validPhaseTransition(a, "failed") || !validPhaseTransition(a, "cancelled") || !validPhaseTransition(a, "interrupted") {
			t.Fatal(a)
		}
	}
	for _, a := range []string{"completed", "failed", "cancelled", "interrupted"} {
		if validPhaseTransition(a, "failed") || validPhaseTransition(a, "queued") {
			t.Fatal(a)
		}
	}
}

// TestJobDurablePhases observes actual barriers and never relies on timing percentages.
func TestJobDurablePhases(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	entered := make(chan string)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	m.phaseHook = func(s string) error { entered <- s; <-release; return nil }
	sub, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	if sub.Job.State != "queued" || sub.Job.StartedAt != nil || sub.Job.ResultSnapshotID != nil {
		t.Fatal(sub)
	}
	states := []string{"queued"}
	for _, want := range []string{"preflight", "scanning", "validating", "publishing"} {
		select {
		case got := <-entered:
			if got != want {
				t.Fatal(got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("phase timeout")
		}
		o, e := m.Get(sub.Job.ID)
		if e != nil || o.Job.State != want || o.Job.ResultSnapshotID != nil {
			t.Fatal(o, e)
		}
		states = append(states, want)
		var actual []string
		for _, p := range o.PhaseHistory {
			actual = append(actual, p.State)
			if _, e := time.Parse(time.RFC3339Nano, p.EnteredAt); e != nil {
				t.Fatal(e)
			}
		}
		if !reflect.DeepEqual(actual, states) {
			t.Fatal(actual, states)
		}
		if o.StoredData == nil || o.StoredData.Quality != "missing" || o.StoredData.Freshness != "unknown" {
			t.Fatal(o.StoredData)
		}
		release <- struct{}{}
	}
	o := terminal(t, m, sub.Job.ID)
	if o.Job.State != "completed" || o.SnapshotState != "complete" || len(o.PhaseHistory) != 6 || o.PhaseHistory[5].State != "completed" || o.StoredData.Quality != "complete" {
		t.Fatal(o)
	}
	var n int
	m.db.QueryRow(`SELECT count(*) FROM job_phases WHERE job_id=?`, o.Job.ID).Scan(&n)
	m.Get(o.Job.ID)
	m.Get(o.Job.ID)
	var after int
	m.db.QueryRow(`SELECT count(*) FROM job_phases WHERE job_id=?`, o.Job.ID).Scan(&after)
	if n != after {
		t.Fatal("poll mutated history")
	}
}

// TestPreflightReservePersistsFailure protects the previous complete pointer and uses actual error codes.
func TestPreflightReservePersistsFailure(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	good, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	baseline := terminal(t, m, good.Job.ID)
	for _, test := range []struct {
		name, code string
		free       uint64
		err        error
	}{{"low", "INDEX_SPACE_LOW", IndexReserveBytes - 1, nil}, {"unavailable", "INDEX_SPACE_UNAVAILABLE", 0, syscall.EIO}} {
		t.Run(test.name, func(t *testing.T) {
			m.spaceProbe = func() (uint64, error) { return test.free, test.err }
			sub, e := m.Start(in.Profile.ID, "full")
			if e != nil {
				t.Fatal(e)
			}
			o := terminal(t, m, sub.Job.ID)
			if o.Job.State != "failed" || o.Job.ResultSnapshotID != nil || len(o.PhaseHistory) != 3 || o.PhaseHistory[1].State != "preflight" {
				t.Fatal(o)
			}
			if o.Job.Warnings[len(o.Job.Warnings)-1].Code != test.code {
				t.Fatal(o.Job.Warnings)
			}
			var active string
			m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active)
			if active != *baseline.Job.ResultSnapshotID {
				t.Fatal(active)
			}
			if ready, code := m.Health(context.Background()); ready || code != test.code {
				t.Fatal(ready, code)
			}
		})
	}
	m.spaceProbe = nil
	if ready, code := m.Health(context.Background()); !ready || code != "" {
		t.Fatal(ready, code)
	}
	invalid := Outcome{Job: contracts.Job{ProfileID: in.Profile.ID, ProfileRevision: 2, CapturedProfile: in.Profile}}
	if e := m.preflight(&invalid); e == nil || e.Error() != "PROFILE_INVALID" {
		t.Fatal(e)
	}
	if executionFailure(syscall.ENOSPC) != "INDEX_SPACE_EXHAUSTED" || executionFailure(errors.New("private /source")) != "SCAN_FAILED" {
		t.Fatal("error redaction")
	}
}

// TestRestartInterruptsEveryNonterminalPhase retains outcomes without resuming fictitious progress.
func TestRestartInterruptsEveryNonterminalPhase(t *testing.T) {
	m, in, _ := fixture(t)
	for _, state := range []string{"queued", "preflight", "scanning", "paused", "validating", "publishing"} {
		o := Outcome{Job: contracts.Job{ID: "restart-" + state, ProfileID: in.Profile.ID, State: state, BytesObserved: "0"}}
		if err := m.save(o); err != nil {
			t.Fatal(err)
		}
	}
	statePath := m.state
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, state := range []string{"queued", "preflight", "scanning", "paused", "validating", "publishing"} {
		o, err := reopened.Get("restart-" + state)
		if err != nil || o.Job.State != "interrupted" || o.Job.EndedAt == nil || len(o.PhaseHistory) != 1 || o.PhaseHistory[0].State != "interrupted" {
			t.Fatal(o, err)
		}
	}
}

// TestSQLiteFullIsConcrete uses a bounded real SQLite capacity limit, not an invented errno.
func TestSQLiteFullIsConcrete(t *testing.T) {
	db, err := index.Open(filepath.Join(t.TempDir(), "limited.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`PRAGMA max_page_count=4; CREATE TABLE limited(value BLOB)`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO limited VALUES(zeroblob(1048576))`)
	if err == nil || executionFailure(err) != "INDEX_SPACE_EXHAUSTED" {
		t.Fatal(err)
	}
}
