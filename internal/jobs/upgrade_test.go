package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
)

// Real offline modes retain cancellation/deadline classification even when migration masks IO errors.
func TestUpgradeModeContextClassification(t *testing.T) {
	for _, mode := range []string{"prepare", "apply", "restore"} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deadline=%v", mode, deadline), func(t *testing.T) {
				m, _, _ := fixture(t)
				state := m.state
				m.Close()
				if mode != "prepare" {
					if err := Upgrade(context.Background(), state, "prepare"); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(filepath.Join(state, "control.db"))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				want := context.Canceled
				if deadline {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					want = context.DeadlineExceeded
				}
				err = Upgrade(ctx, state, mode)
				if !errors.Is(err, want) || !errors.Is(err, migration.ErrUpgrade) {
					t.Fatalf("classification: %v", err)
				}
				after, err := os.ReadFile(filepath.Join(state, "control.db"))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("cancelled mode changed control", err)
				}
			})
		}
	}
}

// A real pre-wrapper control schema upgrades offline while preserving profiles and sealed generation bytes.
func TestOfflineUpgradePreservesProfileAndGeneration(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	job, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, job.Job.ID)
	if done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	state := m.state
	gen := filepath.Join(state, "generations", *done.Job.ResultSnapshotID, "index.db")
	before, _ := os.ReadFile(gen)
	m.Close()
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("DROP TABLE schema_steps; DROP TABLE onboarding_drafts; DROP TABLE exclusion_template; PRAGMA user_version=2")
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if bad, e := Open(context.Background(), state); e != migration.ErrVersion {
		if bad != nil {
			bad.Close()
		}
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !bytes.Equal(original, after) {
		t.Fatal("ordinary startup mutated ungated schema")
	}
	for _, mode := range []string{"prepare", "apply"} {
		if e := Upgrade(context.Background(), state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	next, e := Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	var name string
	if next.db.QueryRow("SELECT json_extract(profile_json,'$.name') FROM profiles WHERE id=?", in.Profile.ID).Scan(&name) != nil || name != in.Profile.Name {
		t.Fatal(name)
	}
	after, _ = os.ReadFile(gen)
	if !bytes.Equal(before, after) {
		t.Fatal("sealed generation changed")
	}
}

// A future control schema is rejected before creating any schema table or worker.
func TestControlSchemaDowngradeRejected(t *testing.T) {
	m, _, _ := fixture(t)
	state := m.state
	m.Close()
	db, _ := sql.Open("sqlite", filepath.Join(state, "control.db"))
	db.Exec("PRAGMA user_version=999")
	db.Close()
	before, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if e := Upgrade(context.Background(), state, "prepare"); e != migration.ErrVersion {
		t.Fatal(e)
	}
	if m, e := Open(context.Background(), state); e != migration.ErrVersion {
		if m != nil {
			m.Close()
		}
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("future database changed")
	}
}

// Proven unsupported formats lose only their default pointer; immutable source generation bytes remain available for explicit recovery.
func TestUpgradeWithdrawsUnsupportedGeneration(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	job, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, job.Job.ID)
	if done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	state := m.state
	gen := filepath.Join(state, "generations", *done.Job.ResultSnapshotID, "index.db")
	m.Close()
	db, _ := sql.Open("sqlite", gen)
	_, e = db.Exec("PRAGMA user_version=999")
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(gen)
	for _, mode := range []string{"prepare", "apply"} {
		if e := Upgrade(context.Background(), state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	db, _ = sql.Open("sqlite", filepath.Join(state, "control.db"))
	defer db.Close()
	var n int
	if db.QueryRow("SELECT count(*) FROM active_generations").Scan(&n) != nil || n != 0 {
		t.Fatal(n)
	}
	if db.QueryRow("SELECT count(*) FROM profiles").Scan(&n) != nil || n != 1 {
		t.Fatal(n)
	}
	after, _ := os.ReadFile(gen)
	if !bytes.Equal(before, after) {
		t.Fatal("unsupported generation altered")
	}
}

// Mode budgets remain bounded while parent deadlines and cancellation take precedence.
func TestUpgradeDeadlineBudgets(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		budget time.Duration
	}{{"apply", 120 * time.Minute}, {"prepare", 2 * time.Minute}, {"restore", 2 * time.Minute}} {
		t.Run(tc.mode, func(t *testing.T) {
			before := time.Now()
			ctx, cancel := upgradeDeadline(context.Background(), tc.mode)
			defer cancel()
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Before(before.Add(tc.budget)) || deadline.After(time.Now().Add(tc.budget)) {
				t.Fatalf("unexpected budget for %s", tc.mode)
			}
			parent, stop := context.WithDeadline(context.Background(), time.Now().Add(time.Minute))
			defer stop()
			child, done := upgradeDeadline(parent, tc.mode)
			defer done()
			pd, _ := parent.Deadline()
			cd, _ := child.Deadline()
			if !cd.Equal(pd) {
				t.Fatal("parent deadline extended")
			}
			stop()
			if child.Err() != context.Canceled {
				t.Fatal("parent cancellation lost")
			}
			cancelled, abort := context.WithCancel(context.Background())
			abort()
			c, close := upgradeDeadline(cancelled, tc.mode)
			defer close()
			if c.Err() != context.Canceled {
				t.Fatal("already cancelled parent ignored")
			}
		})
	}
}

// Version-boundary fixtures retain their bytes: upgrade must reject old formats
// before interpreting their private layout, then permit an explicit rebuild.
func TestUpgradeOldSearchFormatsPreservesAndRebuilds(t *testing.T) {
	for _, control := range []int{5, 6, 7} {
		for _, version := range []int{7, 8, 9, 10, 11} {
			t.Run(fmt.Sprintf("control%d-generation%d", control, version), func(t *testing.T) {
				m, in, _ := fixture(t)
				if e := m.ImportProfile(in); e != nil {
					t.Fatal(e)
				}
				submitted, e := m.StartBrowser(in.Profile.ID, "full", BrowserStartIdentity{Principal: "upgrade-admin", Key: "before-upgrade"})
				if e != nil {
					t.Fatal(e)
				}
				old := terminal(t, m, submitted.Job.ID)
				if old.Job.ResultSnapshotID == nil {
					t.Fatal(old)
				}
				state := m.state
				snapshot := *old.Job.ResultSnapshotID
				m.Close()
				path := filepath.Join(state, "generations", snapshot, "index.db")
				db, e := sql.Open("sqlite", path)
				if e != nil {
					t.Fatal(e)
				}
				// These are unsupported-format fixtures, not claims of recreated historical seals.
				_, e = db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version))
				db.Close()
				if e != nil {
					t.Fatal(e)
				}
				before, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				db, e = sql.Open("sqlite", filepath.Join(state, "control.db"))
				if e != nil {
					t.Fatal(e)
				}
				var profileBefore string
				if e = db.QueryRow("SELECT profile_json FROM profiles WHERE id=?", in.Profile.ID).Scan(&profileBefore); e != nil {
					t.Fatal(e)
				}
				if control == 5 {
					_, e = db.Exec("DROP TABLE onboarding_drafts; DELETE FROM schema_steps WHERE version IN (6,7); PRAGMA user_version=5")
					if e != nil {
						t.Fatal(e)
					}
				}
				db.Close()
				for _, mode := range []string{"prepare", "apply"} {
					if e = Upgrade(context.Background(), state, mode); e != nil {
						t.Fatal(mode, e)
					}
				}
				next, e := Open(context.Background(), state)
				if e != nil {
					t.Fatal(e)
				}
				defer next.Close()
				var profileAfter string
				var pointers int
				if e = next.db.QueryRow("SELECT profile_json FROM profiles WHERE id=?", in.Profile.ID).Scan(&profileAfter); e != nil || profileAfter != profileBefore {
					t.Fatal("profile changed", e)
				}
				if e = next.db.QueryRow("SELECT count(*) FROM active_generations WHERE profile_id=?", in.Profile.ID).Scan(&pointers); e != nil || pointers != 0 {
					t.Fatal("old default survived", e)
				}
				replayed, replayErr := next.StartBrowser(in.Profile.ID, "full", BrowserStartIdentity{Principal: "upgrade-admin", Key: "before-upgrade"})
				if replayErr != nil || replayed.Job.ID != submitted.Job.ID {
					t.Fatal("replay changed", replayErr)
				}
				rebuilt, e := next.Start(in.Profile.ID, "rebuild")
				if e != nil {
					t.Fatal(e)
				}
				done := terminal(t, next, rebuilt.Job.ID)
				if done.Job.State != "completed" || done.Job.ResultSnapshotID == nil || *done.Job.ResultSnapshotID == snapshot || done.Job.SchemaVersion != index.GenerationSchema {
					t.Fatal(done)
				}
				if _, e = next.Snapshot(context.Background(), in.Profile.ID); e != nil {
					t.Fatal(e)
				}
				after, e := os.ReadFile(path)
				if e != nil || !bytes.Equal(before, after) {
					t.Fatal("old generation changed", e)
				}
			})
		}
	}
}
