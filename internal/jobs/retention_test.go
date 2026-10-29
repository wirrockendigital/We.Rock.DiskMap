package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scanRetention creates actual sealed SQLite generations, not mocked qualification receipts.
func scanRetention(t *testing.T, m *Manager, profile string) string {
	t.Helper()
	s, e := m.Start(profile, "full")
	if e != nil {
		t.Fatal(e)
	}
	o := terminal(t, m, s.Job.ID)
	if o.Job.State != "completed" || o.Job.ResultSnapshotID == nil {
		t.Fatal(o)
	}
	return *o.Job.ResultSnapshotID
}
func generationExists(m *Manager, id string) bool {
	_, e := os.Stat(filepath.Join(m.state, "generations", id, "index.db"))
	return e == nil
}

// Count30 preserves thirty complete results; switching to one protects the last usable and live older readers.
func TestRetentionCountsAndLongReader(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if _, e := m.SetRetention(ctx, RetentionRequest{1, 30}, "admin", "keep30"); e != nil {
		t.Fatal(e)
	}
	ids := []string{}
	for range 31 {
		ids = append(ids, scanRetention(t, m, in.Profile.ID))
	}
	m.mu.Lock()
	m.retainGenerations(ctx)
	m.mu.Unlock()
	if generationExists(m, ids[0]) {
		t.Fatal("31st complete still present")
	}
	for _, id := range ids[1:] {
		if !generationExists(m, id) {
			t.Fatal("retained generation removed", id)
		}
	}
	view, e := m.OpenRetainedView(ctx, "admin", in.Profile.ID, "long-reader", ids[1])
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetRetention(ctx, RetentionRequest{1, 1}, "admin", "keep1"); e != nil {
		t.Fatal(e)
	}
	if !generationExists(m, ids[1]) || !generationExists(m, ids[30]) {
		t.Fatal("protected generation removed")
	}
	if e = m.ReleaseView("admin", in.Profile.ID, view.Lease.ID); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.retainGenerations(ctx)
	m.mu.Unlock()
	if generationExists(m, ids[1]) {
		t.Fatal("released old generation not cleaned")
	}
	if !generationExists(m, ids[30]) {
		t.Fatal("last usable removed")
	}
}

// A corrupt active pointer never authorizes pruning an older usable result; low space does not trigger cleanup.
func TestRetentionCorruptActiveAndReserve(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	a := scanRetention(t, m, in.Profile.ID)
	b := scanRetention(t, m, in.Profile.ID)
	if e := os.WriteFile(filepath.Join(m.state, "generations", b, "index.db"), []byte("corrupt"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := m.SetRetention(ctx, RetentionRequest{1, 1}, "admin", "corrupt"); e != nil {
		t.Fatal(e)
	}
	if !generationExists(m, a) {
		t.Fatal("usable predecessor removed")
	}
	m.spaceProbe = func() (uint64, error) { return IndexReserveBytes - 1, nil }
	if e := m.reserve(); e == nil || e.Error() != "INDEX_SPACE_LOW" {
		t.Fatal(e)
	}
	if !generationExists(m, a) {
		t.Fatal("reserve deleted last usable")
	}
}

// File and directory substitutions must be refused without touching foreign data.
func TestRetentionSymlinkAndReplacement(t *testing.T) {
	for _, kind := range []string{"symlink", "inode", "parent"} {
		t.Run(kind, func(t *testing.T) {
			m, in, _ := fixture(t)
			if e := m.ImportProfile(in); e != nil {
				t.Fatal(e)
			}
			id := scanRetention(t, m, in.Profile.ID)
			var job string
			if e := m.db.QueryRow(`SELECT id FROM jobs WHERE json_extract(outcome_json,'$.job.resultSnapshotId')=?`, id).Scan(&job); e != nil {
				t.Fatal(e)
			}
			dir := filepath.Join(m.state, "generations", id)
			foreign := filepath.Join(t.TempDir(), "foreign")
			if e := os.WriteFile(foreign, []byte("preserve-me"), 0600); e != nil {
				t.Fatal(e)
			}
			_, _ = m.db.Exec(`INSERT INTO retention_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, in.Profile.ID, id, job)
			e := m.removeRetainedGenerationChecked(context.Background(), in.Profile.ID, id, job, "", func() {
				switch kind {
				case "symlink":
					_ = os.Rename(filepath.Join(dir, "index.db"), filepath.Join(dir, "original"))
					_ = os.Symlink(foreign, filepath.Join(dir, "index.db"))
				case "inode":
					_ = os.Rename(filepath.Join(dir, "index.db"), filepath.Join(dir, "original"))
					_ = os.WriteFile(filepath.Join(dir, "index.db"), []byte("replacement"), 0600)
				case "parent":
					_ = os.Rename(dir, dir+"-original")
					_ = os.Symlink(filepath.Dir(foreign), dir)
				}
			})
			if e == nil {
				t.Fatal("replacement accepted")
			}
			raw, e := os.ReadFile(foreign)
			if e != nil || string(raw) != "preserve-me" {
				t.Fatal("foreign changed", e)
			}
		})
	}
}

// Historical qualification runs outside the manager lock and produces current file-bound evidence.
func TestRetentionQualificationAndReplay(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	id := scanRetention(t, m, in.Profile.ID)
	_, _ = m.db.Exec(`UPDATE retention_qualification SET stamp='' WHERE snapshot_id=?`, id)
	m.qualifyRetentionStep(ctx)
	var stamp string
	_ = m.db.QueryRow(`SELECT stamp FROM retention_qualification WHERE snapshot_id=?`, id).Scan(&stamp)
	if stamp == "" {
		t.Fatal("qualification missing")
	}
	receipt, e := m.SetRetention(ctx, RetentionRequest{1, 3}, "admin", "same")
	if e != nil {
		t.Fatal(e)
	}
	again, e := m.SetRetention(ctx, RetentionRequest{1, 3}, "admin", "same")
	if e != nil || receipt != again {
		t.Fatal(again, e)
	}
	if _, e = m.SetRetention(ctx, RetentionRequest{1, 7}, "admin", "same"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	for _, n := range []int{0, 4, 29, 31} {
		if (RetentionRequest{1, n}).Validate() == nil {
			t.Fatal(n)
		}
	}
	out, e := m.Storage(ctx, in.Profile.ID)
	if e != nil || out.FreeBytes == nil || out.IndexBytes == nil || out.MeasurementState != "complete" || len(out.RetainedSnapshots) != 1 {
		t.Fatal(out, e)
	}
	if _, e = time.Parse(time.RFC3339Nano, out.MeasuredAt); e != nil {
		t.Fatal(e)
	}
}

// Measurement separates real staging bytes and journals; symlinked data is never followed or called complete.
func TestStorageMeasurement(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(root, "generations", "x.staging"), 0700)
	_ = os.WriteFile(filepath.Join(root, "control.db"), make([]byte, 11), 0600)
	_ = os.WriteFile(filepath.Join(root, "generations", "x.staging", "index.db"), make([]byte, 17), 0600)
	_ = os.WriteFile(filepath.Join(root, "control.db-journal"), make([]byte, 23), 0600)
	s, e := measureStorage(context.Background(), root)
	if e != nil || s[0].String() != "11" || s[1].String() != "17" || s[2].String() != "23" {
		t.Fatal(s, e)
	}
	_ = os.Symlink(t.TempDir(), filepath.Join(root, "foreign"))
	if _, e = measureStorage(context.Background(), root); e == nil {
		t.Fatal("symlink called complete")
	}
}

// Historical opening must bind its exact target, reject mixed partial requests and keep current defaults unchanged.
func TestRetainedOpenBinding(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	first := scanRetention(t, m, in.Profile.ID)
	second := scanRetention(t, m, in.Profile.ID)
	a, e := m.OpenRetainedView(ctx, "admin", in.Profile.ID, "historical", first)
	if e != nil || a.Lease.SnapshotID != first {
		t.Fatal(a, e)
	}
	if _, e = m.OpenRetainedView(ctx, "admin", in.Profile.ID, "historical", second); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	if _, e = m.OpenRetainedView(ctx, "other", in.Profile.ID, "historical", first); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	current, e := m.Snapshot(ctx, in.Profile.ID)
	if e != nil || *current.SnapshotID != second {
		t.Fatal(current, e)
	}
}

// Unsupported prototype formats are persistently protected instead of consuming qualification retries forever.
func TestRetentionUnsupportedAndRestart(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	id := scanRetention(t, m, in.Profile.ID)
	db, e := sql.Open("sqlite", filepath.Join(m.state, "generations", id, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`PRAGMA user_version=6`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	_, _ = m.db.Exec(`UPDATE retention_qualification SET stamp='' WHERE snapshot_id=?`, id)
	m.qualifyRetentionStep(ctx)
	var stamp string
	_ = m.db.QueryRow(`SELECT stamp FROM retention_qualification WHERE snapshot_id=?`, id).Scan(&stamp)
	if stamp != "unsupported" {
		t.Fatal(stamp)
	}
	out, e := m.Storage(ctx, in.Profile.ID)
	if e != nil || out.UnsupportedGenerations != 1 || len(out.RetainedSnapshots) != 0 {
		t.Fatal(out, e)
	}
	if _, e = m.SetRetention(ctx, RetentionRequest{1, 7}, "admin", "restart-key"); e != nil {
		t.Fatal(e)
	}
	state := m.state
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	next, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	receipt, e := next.SetRetention(ctx, RetentionRequest{1, 7}, "admin", "restart-key")
	if e != nil || receipt.CompleteCount != 7 {
		t.Fatal(receipt, e)
	}
	if _, e = next.SetRetention(ctx, RetentionRequest{1, 1}, "admin", "restart-key"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	if !generationExists(next, id) {
		t.Fatal("unsupported deleted")
	}
}

// A permanently blocked batch cannot prevent later qualified generations from being cleaned.
func TestRetentionCleanupFairness(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	old := scanRetention(t, m, in.Profile.ID)
	latest := scanRetention(t, m, in.Profile.ID)
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.db.Exec(`UPDATE retention_settings SET complete_count=1`)
	for i := range 201 {
		id := fmt.Sprintf("blocked-%03d", i)
		dir := filepath.Join(m.state, "generations", id)
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "unqualified"), []byte("preserve"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := m.db.Exec(`INSERT INTO retention_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, in.Profile.ID, id, "blocked-job"); e != nil {
			t.Fatal(e)
		}
	}
	m.retainGenerations(ctx)
	if !generationExists(m, old) {
		t.Fatal("test did not exercise blocked first batch")
	}
	m.retainGenerations(ctx)
	if generationExists(m, old) {
		t.Fatal("eligible generation starved")
	}
	if !generationExists(m, latest) {
		t.Fatal("active removed")
	}
	for i := range 201 {
		raw, e := os.ReadFile(filepath.Join(m.state, "generations", fmt.Sprintf("blocked-%03d", i), "unqualified"))
		if e != nil || string(raw) != "preserve" {
			t.Fatal("blocked object modified", i, e)
		}
	}
}

// Initial opening and switching share the same evidence requirement for historical targets.
func TestRetainedSwitchQualification(t *testing.T) {
	for _, kind := range []string{"pending", "changed"} {
		t.Run(kind, func(t *testing.T) {
			m, in, _ := fixture(t)
			if e := m.ImportProfile(in); e != nil {
				t.Fatal(e)
			}
			ctx := context.Background()
			old := scanRetention(t, m, in.Profile.ID)
			latest := scanRetention(t, m, in.Profile.ID)
			current, e := m.OpenView(ctx, "admin", in.Profile.ID, "current", "", false)
			if e != nil || current.Lease.SnapshotID != latest {
				t.Fatal(current, e)
			}
			m.mu.Lock()
			if kind == "pending" {
				_, _ = m.db.Exec(`UPDATE retention_qualification SET stamp='' WHERE snapshot_id=?`, old)
			} else {
				p := filepath.Join(m.state, "generations", old, "index.db")
				if e = os.Chtimes(p, time.Now(), time.Now().Add(time.Second)); e != nil {
					t.Fatal(e)
				}
			}
			m.mu.Unlock()
			if _, e = m.OpenRetainedView(ctx, "admin", in.Profile.ID, "direct", old); !errors.Is(e, ErrUnavailable) {
				t.Fatal("initial", e)
			}
			if _, e = m.SwitchView(ctx, "admin", in.Profile.ID, "current", "switched", old, ""); !errors.Is(e, ErrUnavailable) {
				t.Fatal("switch", e)
			}
		})
	}
}

// A maintenance wait longer than the internal budget leaves a fresh metadata budget after admission.
func TestStorageBudgetStartsAfterMaintenanceLock(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); _, err := m.Storage(context.Background(), in.Profile.ID); result <- err }()
	<-started
	time.Sleep(1750 * time.Millisecond)
	m.mu.Unlock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("storage did not finish after maintenance")
	}
}

// Admission cannot revive an outer request that expired while waiting for maintenance.
func TestStoragePreservesParentDeadline(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	m.mu.Lock()
	result := make(chan error, 1)
	go func() { _, err := m.Storage(ctx, in.Profile.ID); result <- err }()
	<-ctx.Done()
	m.mu.Unlock()
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired parent: %v", err)
	}
}
