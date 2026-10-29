package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// checkedIntegrity waits for actual worker completion, not an elapsed-time success assumption.
func checkedIntegrity(t *testing.T, m *Manager, profile, snapshot string) IntegrityEntry {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m.integrityStep(context.Background())
		s, e := m.IntegrityStatus(context.Background(), profile)
		if e != nil {
			t.Fatal(e)
		}
		for _, g := range s.Generations {
			if g.SnapshotID == snapshot && (g.OperationState == "completed" || g.OperationState == "failed") {
				return g
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("integrity operation did not finish")
	return IntegrityEntry{}
}

// Checks preserve sealed bytes; explicit deletion preserves the profile, sources and exact replay receipt.
func TestIntegrityCheckDeleteReplayAndReaders(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	path := filepath.Join(m.state, "generations", snapshot, "index.db")
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	check := IntegrityRequest{ContractVersion: 1, Operation: "check", ProfileID: in.Profile.ID, SnapshotID: snapshot}
	receipt, e := m.IntegrityAction(ctx, check, "admin", "check1")
	if e != nil {
		t.Fatal(e)
	}
	if g := checkedIntegrity(t, m, in.Profile.ID, snapshot); g.Quality != "healthy" || !g.Deletable {
		t.Fatal(g)
	}
	after, e := os.ReadFile(path)
	if e != nil || string(before) != string(after) {
		t.Fatal("check changed sealed database")
	}
	repeat, e := m.IntegrityAction(ctx, check, "admin", "check1")
	if e != nil || repeat != receipt {
		t.Fatal("check replay", repeat, e)
	}
	wrong := check
	wrong.SnapshotID = "foreign"
	if _, e = m.IntegrityAction(ctx, wrong, "admin", "check1"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	lease, e := m.OpenView(ctx, "admin", in.Profile.ID, "held-integrity-view", "", false)
	if e != nil {
		t.Fatal(e)
	}
	name := in.Profile.Name
	del := IntegrityRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, SnapshotID: snapshot, ConfirmationName: &name}
	if _, e = m.IntegrityAction(ctx, del, "admin", "delete1"); !errors.Is(e, ErrConflict) {
		t.Fatal("held reader", e)
	}
	if e = m.ReleaseView("admin", in.Profile.ID, lease.Lease.ID); e != nil {
		t.Fatal(e)
	}
	wrongName := "wrong"
	wrongDelete := del
	wrongDelete.ConfirmationName = &wrongName
	if _, e = m.IntegrityAction(ctx, wrongDelete, "admin", "wrong-name"); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	deletion, e := m.IntegrityAction(ctx, del, "admin", "delete1")
	if e != nil {
		t.Fatal(e)
	}
	g := checkedIntegrity(t, m, in.Profile.ID, snapshot)
	if g.Quality != "deleted" {
		t.Fatal(g)
	}
	if generationExists(m, snapshot) {
		t.Fatal("generation remains")
	}
	if raw, e := os.ReadFile(filepath.Join(source, "data")); e != nil || string(raw) != "abc" {
		t.Fatal("source changed")
	}
	ps, e := m.Profiles(ctx)
	if e != nil || len(ps) != 1 {
		t.Fatal("profile changed", e)
	}
	if repeat, e := m.IntegrityAction(ctx, del, "admin", "delete1"); e != nil || repeat != deletion {
		t.Fatal("delete replay after unlink", repeat, e)
	}
}

// Manifest corruption and unsupported versions remain distinct and cannot become usable through a held lease.
func TestIntegrityCorruptionUnsupportedAndHeldLease(t *testing.T) {
	for _, kind := range []string{"manifest", "schema", "sqlite"} {
		t.Run(kind, func(t *testing.T) {
			m, in, _ := fixture(t)
			ctx := context.Background()
			if e := m.ImportProfile(in); e != nil {
				t.Fatal(e)
			}
			snapshot := scanRetention(t, m, in.Profile.ID)
			_, e := m.OpenView(ctx, "admin", in.Profile.ID, "open-before-corrupt", "", false)
			if e != nil {
				t.Fatal(e)
			}
			db, e := index.Open(filepath.Join(m.state, "generations", snapshot, "index.db"))
			if e != nil {
				t.Fatal(e)
			}
			if kind == "sqlite" {
				db.Close()
				var file *os.File
				file, e = os.OpenFile(filepath.Join(m.state, "generations", snapshot, "index.db"), os.O_WRONLY, 0)
				if e == nil {
					_, e = file.WriteAt([]byte("not-a-sqlite-db!!"), 0)
					file.Close()
				}
			} else if kind == "schema" {
				_, e = db.Exec(`PRAGMA user_version=999`)
			} else {
				_, e = db.Exec(`DROP TRIGGER seal_header_UPDATE; UPDATE generation_header SET header_json=json_set(header_json,'$.logicalBytes','999') WHERE singleton=1`)
			}
			db.Close()
			if e != nil {
				t.Fatal(e)
			}
			_, e = m.IntegrityAction(ctx, IntegrityRequest{ContractVersion: 1, Operation: "check", ProfileID: in.Profile.ID, SnapshotID: snapshot}, "admin", "damaged-check")
			if e != nil {
				t.Fatal(e)
			}
			g := checkedIntegrity(t, m, in.Profile.ID, snapshot)
			want := "corrupt"
			if kind == "schema" {
				want = "unsupported"
			}
			if g.Quality != want {
				t.Fatal(g)
			}
			if _, e = m.OpenView(ctx, "admin", in.Profile.ID, "open-before-corrupt", "", true); !errors.Is(e, ErrViewExpired) {
				t.Fatal("known invalid held lease remained readable", e)
			}
			if _, e = m.Snapshot(ctx, in.Profile.ID); e == nil {
				t.Fatal("invalid snapshot remained readable")
			}
		})
	}
}

// Recovery consumes durable queued intent without requiring the original HTTP request or deleting sources.
func TestIntegrityQueuedDeleteRestart(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	name := in.Profile.Name
	request := IntegrityRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, SnapshotID: snapshot, ConfirmationName: &name}
	receipt, e := m.IntegrityAction(ctx, request, "admin", "restart-delete")
	if e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	next, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if g := checkedIntegrity(t, next, in.Profile.ID, snapshot); g.Quality != "deleted" {
		t.Fatal(g)
	}
	if again, e := next.IntegrityAction(ctx, request, "admin", "restart-delete"); e != nil || again != receipt {
		t.Fatal(again, e)
	}
}

// An identity replacement or extra package-directory entry never widens a deletion into source cleanup.
func TestIntegrityDeleteRejectsReplacement(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	name := in.Profile.Name
	receipt, e := m.IntegrityAction(ctx, IntegrityRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, SnapshotID: snapshot, ConfirmationName: &name}, "admin", "replacement-delete")
	if e != nil {
		t.Fatal(e)
	}
	var w integrityWork
	w.snapshot = snapshot
	w.profile = in.Profile.ID
	if e = m.db.QueryRow(`SELECT stamp,binding FROM integrity_operations WHERE id=?`, receipt.OperationID).Scan(&w.stamp, &w.binding); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(m.state, "generations", snapshot, "index.db")
	if e = os.Rename(path, path+".kept"); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(filepath.Join(source, "data"), path); e != nil {
		t.Fatal(e)
	}
	m.mu.Lock()
	e = m.removeIntegrityGeneration(ctx, w)
	m.mu.Unlock()
	if e == nil {
		t.Fatal("unsafe deletion accepted")
	}
	if raw, e := os.ReadFile(filepath.Join(source, "data")); e != nil || string(raw) != "abc" {
		t.Fatal("source changed")
	}
}

// Cancellation never reports corruption or health, and strict actions never silently accept confirmation on checks.
func TestIntegrityUnknownAndStrictContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if integrityQuality(ctx, nil) != "unverified" {
		t.Fatal("cancelled check marked healthy")
	}
	if integrityQuality(context.Background(), errors.New("arbitrary IO")) != "unverified" {
		t.Fatal("unknown IO marked corrupt")
	}
	name := "x"
	if (IntegrityRequest{1, "check", "p", "s", &name}).Validate() == nil {
		t.Fatal("ignored confirmation")
	}
	var out IntegrityRequest
	if json.Unmarshal([]byte(`{"contractVersion":1,"operation":"check","profileId":"p","snapshotId":"s"}`), &out) != nil || out.Validate() != nil {
		t.Fatal("check invalid")
	}
}

// A post-check file change invalidates its health receipt and read access until a new verified result.
func TestIntegrityHealthyEvidenceBoundToCurrentFile(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	_, e := m.IntegrityAction(ctx, IntegrityRequest{ContractVersion: 1, Operation: "check", ProfileID: in.Profile.ID, SnapshotID: snapshot}, "admin", "health-stamp")
	if e != nil {
		t.Fatal(e)
	}
	if g := checkedIntegrity(t, m, in.Profile.ID, snapshot); g.Quality != "healthy" {
		t.Fatal(g)
	}
	path := filepath.Join(m.state, "generations", snapshot, "index.db")
	if e = os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	status, e := m.IntegrityStatus(ctx, in.Profile.ID)
	if e != nil || status.Generations[0].Quality != "unverified" {
		t.Fatal(status, e)
	}
	if _, e = m.Snapshot(ctx, in.Profile.ID); e == nil {
		t.Fatal("stale health receipt accepted")
	}
}

// Unsupported input is left byte-identical and a new native scan supplies recovery without conversion.
func TestIntegrityUnsupportedRecoveryPreservesOriginalProfile(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	old := scanRetention(t, m, in.Profile.ID)
	path := filepath.Join(m.state, "generations", old, "index.db")
	db, e := index.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`PRAGMA user_version=999`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = m.IntegrityAction(ctx, IntegrityRequest{ContractVersion: 1, Operation: "check", ProfileID: in.Profile.ID, SnapshotID: old}, "admin", "unsupported-native")
	if e != nil {
		t.Fatal(e)
	}
	if g := checkedIntegrity(t, m, in.Profile.ID, old); g.Quality != "unsupported" {
		t.Fatal(g)
	}
	after, e := os.ReadFile(path)
	if e != nil || string(before) != string(after) {
		t.Fatal("unsupported format modified")
	}
	submission, e := m.Start(in.Profile.ID, "rebuild")
	if e != nil {
		t.Fatal(e)
	}
	out := terminal(t, m, submission.Job.ID)
	if out.Job.State != "completed" || out.Job.ResultSnapshotID == nil || *out.Job.ResultSnapshotID == old {
		t.Fatal(out)
	}
	if _, e = m.Snapshot(ctx, in.Profile.ID); e != nil {
		t.Fatal("rebuild unusable", e)
	}
	if data, e := os.ReadFile(filepath.Join(source, "data")); e != nil || string(data) != "abc" {
		t.Fatal("source changed")
	}
}

// Even a confirmed deletion cannot remove a snapshot while its profile has an active scan.
func TestIntegrityDeleteBlocksActiveJob(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	entered, release := phaseBarrier(t, m, "scanning")
	defer release()
	sub, e := m.Start(in.Profile.ID, "rebuild")
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scan barrier not reached")
	}
	name := in.Profile.Name
	_, e = m.IntegrityAction(ctx, IntegrityRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, SnapshotID: snapshot, ConfirmationName: &name}, "admin", "active-delete")
	if !errors.Is(e, ErrProfileActive) {
		t.Fatal(e)
	}
	if !generationExists(m, snapshot) {
		t.Fatal("active profile index deleted")
	}
	release()
	terminal(t, m, sub.Job.ID)
}
