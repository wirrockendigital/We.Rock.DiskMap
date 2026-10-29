package jobs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// A held real writer must expose only the preceding committed snapshot to every observation API.
func TestControlWALReadersKeepCommittedState(t *testing.T) {
	m, profile, job := observedFixture(t)
	waitQueryFixtureDispatch(t, m)
	view, err := m.OpenView(context.Background(), "admin", profile, "wal-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := m.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var mode string
	var durability int
	if err = conn.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatal(mode, err)
	}
	if err = conn.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&durability); err != nil || durability != 2 {
		t.Fatal(durability, err)
	}
	if _, err = conn.ExecContext(context.Background(), `BEGIN IMMEDIATE; UPDATE jobs SET state='scanning',outcome_json=json_set(outcome_json,'$.job.state','scanning') WHERE id=?`, job); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	status, err := m.Status(ctx)
	if err != nil || status.Active != nil {
		t.Fatal(status, err)
	}
	outcome, err := m.ObserveJob(ctx, job)
	if err != nil || outcome.Job.State != "completed" {
		t.Fatal(outcome.Job.State, err)
	}
	progress, err := m.Progress(ctx, job)
	if err != nil || progress.State != "completed" {
		t.Fatal(progress.State, err)
	}
	history, err := m.History(ctx, profile)
	if err != nil || len(history.Jobs) == 0 || history.Jobs[0].State != "completed" {
		t.Fatal(history, err)
	}
	if _, err = m.ScopeObservations(ctx, ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{ShareID: "share", Path: "."}}}); err != nil {
		t.Fatal(err)
	}
	request := HierarchyRequest{ContractVersion: 1, ProfileID: profile, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *view.View.SnapshotID, RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 1}}
	if _, err = m.ReadHierarchy(ctx, "admin", request, false); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadMapManifest(ctx, "admin", MapRequest{ContractVersion: request.ContractVersion, ProfileID: request.ProfileID, ViewID: request.ViewID, Query: request.Query}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("committed observations blocked by writer: %s", elapsed)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		info, e := os.Stat(filepath.Join(m.state, "control.db") + suffix)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal(suffix, info, e)
		}
	}
}

// Clean shutdown checkpoints all committed control state before the offline package-upgrade boundary.
func TestControlWALCloseCheckpoints(t *testing.T) {
	m, _, _ := observedFixture(t)
	state := m.state
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(filepath.Join(state, "control.db") + suffix); !os.IsNotExist(err) {
			t.Fatal("live sidecar after clean close", suffix, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(state, "control.db"))
	if err != nil || len(raw) < 20 || raw[18] != 1 || raw[19] != 1 {
		t.Fatal("cold database still depends on WAL", err)
	}
	reopened, err := Open(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var count int
	if err = reopened.db.QueryRow("SELECT count(*) FROM profiles").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

// Process exit without SQLite Close leaves a real WAL that startup must recover without losing committed settings.
func TestControlWALCrashRecovery(t *testing.T) {
	const key = "DISKMAP_TEST_WAL_CRASH_STATE"
	if state := os.Getenv(key); state != "" {
		m, err := Open(context.Background(), state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.db.Exec("UPDATE retention_settings SET complete_count=3"); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	state := filepath.Join(t.TempDir(), "state")
	command := exec.Command(os.Args[0], "-test.run=^TestControlWALCrashRecovery$")
	command.Env = append(os.Environ(), key+"="+state)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	if _, err := os.Stat(filepath.Join(state, "control.db-wal")); err != nil {
		t.Fatal("fixture did not retain crash WAL", err)
	}
	m, err := Open(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var count int
	if err = m.db.QueryRow("SELECT complete_count FROM retention_settings").Scan(&count); err != nil || count != 3 {
		t.Fatal("committed setting lost", count, err)
	}
}

// Sidecar names must never cause SQLite to follow a foreign inode during startup or crash recovery.
func TestControlWALRejectsAliasedSidecars(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			m, _, _ := observedFixture(t)
			state := m.state
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			foreign := filepath.Join(t.TempDir(), "foreign")
			if err := os.WriteFile(foreign, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(foreign, filepath.Join(state, "control.db")+suffix); err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(context.Background(), state); err == nil {
				opened.Close()
				t.Fatal("aliased sidecar admitted")
			}
			raw, err := os.ReadFile(foreign)
			if err != nil || string(raw) != "unchanged" {
				t.Fatal("foreign inode changed", err)
			}
		})
	}
}
