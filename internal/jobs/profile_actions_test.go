package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// actionConfiguration keeps test actions on the same canonical public configuration schema.
func actionConfiguration(p contracts.Profile) *profiles.Configuration {
	return &profiles.Configuration{Name: p.Name, Scope: p.Scope, Active: p.Active, Exclusions: p.Exclusions, ResourceMode: p.ResourceMode, Limits: p.Limits, FreshnessSeconds: p.FreshnessSeconds}
}

// TestProfileActionsReplayAndRevision exercises real SQLite domain state across concurrent requests and restart.
func TestProfileActionsReplayAndRevision(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	ctx := context.Background()
	create := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	var wg sync.WaitGroup
	outputs := make([]ProfileActionResult, 8)
	errs := make([]error, 8)
	for n := range outputs {
		wg.Add(1)
		go func(n int) { defer wg.Done(); outputs[n], errs[n] = m.ProfileAction(ctx, create, "admin", "create") }(n)
	}
	wg.Wait()
	for n, out := range outputs {
		if errs[n] != nil || out.ProfileID == in.Profile.ID || !reflect.DeepEqual(out, outputs[0]) {
			t.Fatal(n, out, errs[n])
		}
	}
	id := outputs[0].ProfileID
	other, err := m.ProfileAction(ctx, create, "other-admin", "create")
	if err != nil || other.ProfileID == id {
		t.Fatal(other, err)
	}
	create.Configuration.Name = "Changed intent"
	if _, err = m.ProfileAction(ctx, create, "admin", "create"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	duplicate := profiles.ActionRequest{ContractVersion: 1, Operation: "duplicate", ProfileID: id, ExpectedRevision: 1, Name: "Copy"}
	copy, err := m.ProfileAction(ctx, duplicate, "admin", "duplicate")
	if err != nil || copy.ProfileID == id || copy.Profile.Revision != 1 {
		t.Fatal(copy, err)
	}
	update := profiles.ActionRequest{ContractVersion: 1, Operation: "update", ProfileID: id, ExpectedRevision: 1, Configuration: actionConfiguration(*outputs[0].Profile)}
	update.Configuration.Name = "Renamed"
	renamed, err := m.ProfileAction(ctx, update, "admin", "rename")
	if err != nil || renamed.Profile.Revision != 1 || renamed.ProfileID != id {
		t.Fatal(renamed, err)
	}
	update.Configuration.Exclusions = []contracts.Exclusion{{Kind: "pattern", Value: "*.tmp"}}
	updated, err := m.ProfileAction(ctx, update, "admin", "update")
	if err != nil || updated.Profile.Revision != 2 {
		t.Fatal(updated, err)
	}
	if _, err = m.ProfileAction(ctx, update, "admin", "stale"); !errors.Is(err, ErrProfileConflict) {
		t.Fatal(err)
	}
	deactivate := profiles.ActionRequest{ContractVersion: 1, Operation: "deactivate", ProfileID: id, ExpectedRevision: 2}
	off, err := m.ProfileAction(ctx, deactivate, "admin", "off")
	if err != nil || off.Profile.Active {
		t.Fatal(off, err)
	}
	if _, err = m.Start(id, "full"); !errors.Is(err, ErrInvalid) {
		t.Fatal("inactive scan", err)
	}
	var jobs int
	m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs)
	if jobs != 0 {
		t.Fatal("CRUD launched a scan")
	}
	if raw, err := os.ReadFile(filepath.Join(source, "data")); err != nil || string(raw) != "abc" {
		t.Fatal("source modified", err)
	}
	state := m.state
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	resumed.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	replay, err := resumed.ProfileAction(ctx, duplicate, "admin", "duplicate")
	if err != nil || !reflect.DeepEqual(replay, copy) {
		t.Fatal(replay, err)
	}
}

// TestProfileMutationRollback proves replay persistence failure rolls back the real configuration write.
func TestProfileMutationRollback(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	if _, err := m.db.Exec(`CREATE TRIGGER fail_profile_replay BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	in.Profile.Name = "Rollback"
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "update", ProfileID: in.Profile.ID, ExpectedRevision: 1, Configuration: actionConfiguration(in.Profile)}
	if _, err := m.ProfileAction(ctx, req, "admin", "rollback"); err == nil {
		t.Fatal("expected transaction failure")
	}
	list, err := m.Profiles(ctx)
	if err != nil || list[0].Profile.Name == "Rollback" {
		t.Fatal(list, err)
	}
	var n int
	m.db.QueryRow(`SELECT count(*) FROM idempotency`).Scan(&n)
	if n != 0 {
		t.Fatal("replay committed alone")
	}
}

// TestProfileDeleteHistoricalGenerationsAndReader proves complete and partial generations are separately retired.
func TestProfileDeleteHistoricalGenerationsAndReader(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	ctx := context.Background()
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	first := terminal(t, m, start.Job.ID)
	// An unavailable selected target yields a genuine partial generation without touching source data.
	catalog.Volumes[0].State = "unmounted"
	catalog.Shares[0].State = "missing"
	catalog.Shares[0].Readable = false
	start, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	second := terminal(t, m, start.Job.ID)
	if first.SnapshotState != "complete" || second.SnapshotState != "partial" {
		t.Fatal(first, second)
	}
	m.mu.Lock()
	db, release, err := m.generationDB(*first.Job.ResultSnapshotID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, ExpectedRevision: 1, ConfirmOwnDataDeletion: true, AcknowledgeTasks: true, ConfirmProfileName: in.Profile.Name}
	receipt, err := m.ProfileAction(ctx, req, "admin", "delete")
	if err != nil || !receipt.CleanupScheduled {
		t.Fatal(receipt, err)
	}
	status, err := m.ProfileCleanupStatus(ctx, in.Profile.ID)
	if err != nil || status.PendingGenerations != 1 || status.Complete {
		t.Fatal(status, err)
	}
	var nodes int
	if err = db.QueryRow(`SELECT count(*) FROM nodes`).Scan(&nodes); err != nil || nodes < 1 {
		t.Fatal("reader invalidated", err)
	}
	release()
	status, err = m.ProfileCleanupStatus(ctx, in.Profile.ID)
	if err != nil || !status.Complete {
		t.Fatal(status, err)
	}
	for _, out := range []Outcome{first, second} {
		if _, err = os.Lstat(filepath.Join(m.state, "generations", *out.Job.ResultSnapshotID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("generation remains", err)
		}
	}
	if _, err = m.Start(in.Profile.ID, "full"); err == nil {
		t.Fatal("deleted profile scanned")
	}
	replay, err := m.ProfileAction(ctx, req, "admin", "delete")
	if err != nil || !reflect.DeepEqual(receipt, replay) {
		t.Fatal(replay, err)
	}
	req.ConfirmProfileName = "other"
	if _, err = m.ProfileAction(ctx, req, "admin", "delete"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if err = m.ImportProfile(in); err == nil {
		t.Fatal("retired ID resurrected")
	}
	if raw, err := os.ReadFile(filepath.Join(source, "data")); err != nil || string(raw) != "abc" {
		t.Fatal("source changed", err)
	}
}

// TestProfileDeleteRejectsConfirmationAndActiveJob tests denial before any tombstone/replay write.
func TestProfileDeleteRejectsConfirmationAndActiveJob(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, ExpectedRevision: 1}
	if _, err := m.ProfileAction(ctx, req, "admin", "delete"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	req.ConfirmOwnDataDeletion = true
	req.AcknowledgeTasks = true
	req.ConfirmProfileName = in.Profile.Name
	m.active = &Outcome{Job: contracts.Job{ProfileID: in.Profile.ID}}
	if _, err := m.ProfileAction(ctx, req, "admin", "delete"); !errors.Is(err, ErrProfileActive) {
		t.Fatal(err)
	}
	m.active = nil
	var n int
	m.db.QueryRow(`SELECT count(*) FROM profile_tombstones`).Scan(&n)
	if n != 0 {
		t.Fatal("denial mutated state")
	}
}

// TestProfileCleanupRejectsForeignHeaderAndRecoversOnRestart never unlinks a differently bound generation.
func TestProfileCleanupRejectsForeignHeaderAndRecoversOnRestart(t *testing.T) {
	m, in, source, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, start.Job.ID)
	// A mismatched queue cannot gain ownership merely by naming an existing snapshot.
	_, err = m.db.Exec(`INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, "foreign", *done.Job.ResultSnapshotID, done.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cleanupProfiles(ctx)
	m.mu.Unlock()
	if _, err = os.Stat(filepath.Join(m.state, "generations", *done.Job.ResultSnapshotID, "index.db")); err != nil {
		t.Fatal(err)
	}
	m.db.Exec(`DELETE FROM profile_cleanup WHERE profile_id='foreign'`)
	m.mu.Lock()
	_, release, err := m.generationDB(*done.Job.ResultSnapshotID)
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, ExpectedRevision: 1, ConfirmOwnDataDeletion: true, AcknowledgeTasks: true, ConfirmProfileName: in.Profile.Name}
	receipt, err := m.ProfileAction(ctx, req, "admin", "delete")
	if err != nil {
		t.Fatal(err)
	}
	release()
	state := m.state
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	status, err := next.ProfileCleanupStatus(ctx, in.Profile.ID)
	if err != nil || !status.Complete {
		t.Fatal(status, err)
	}
	repeated, err := next.ProfileAction(ctx, req, "admin", "delete")
	a, _ := json.Marshal(receipt)
	b, _ := json.Marshal(repeated)
	if err != nil || string(a) != string(b) {
		t.Fatal(repeated, err)
	}
	if _, err = os.Stat(filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
}

// TestProfileDeleteFindsPublishedOrphan covers the exact rename-before-job-save crash window.
func TestProfileDeleteFindsPublishedOrphan(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, start.Job.ID)
	snapshot := *done.Job.ResultSnapshotID
	done.Job.ResultSnapshotID = nil
	done.Job.State = "interrupted"
	if err = m.save(done); err != nil {
		t.Fatal(err)
	}
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, ExpectedRevision: 1, ConfirmOwnDataDeletion: true, AcknowledgeTasks: true, ConfirmProfileName: in.Profile.Name}
	if _, err = m.ProfileAction(ctx, req, "admin", "orphan"); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(m.state, "generations", snapshot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published orphan remains", err)
	}
}

// TestCleanupSymlinkAndPostUnlinkRecovery proves no-follow deletion and the durable unlink receipt.
func TestCleanupSymlinkAndPostUnlinkRecovery(t *testing.T) {
	m, in, source, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, start.Job.ID)
	snapshot := *done.Job.ResultSnapshotID
	dir := filepath.Join(m.state, "generations", snapshot)
	held, binding, err := scanner.OpenBoundRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	held.Close()
	encoded, _ := json.Marshal(binding)
	if _, err = m.db.Exec(`INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id,binding_json) VALUES(?,?,?,?)`, in.Profile.ID, snapshot, done.Job.ID, string(encoded)); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(dir, "index.db")); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cleanupProfiles(ctx)
	m.mu.Unlock()
	if _, err = os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	// A matching-looking generation name pointing outside package state is never followed.
	if err = os.Symlink(source, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, in.Profile.ID, snapshot, done.Job.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cleanupProfiles(ctx)
	m.mu.Unlock()
	if info, err := os.Lstat(dir); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link mutated", err)
	}
	if raw, err := os.ReadFile(filepath.Join(source, "data")); err != nil || string(raw) != "abc" {
		t.Fatal("source changed", err)
	}
}

// TestProfileActionsFailClosedOnSourceFailure proves no profile/replay is published from unavailable inventory.
func TestProfileActionsFailClosedOnSourceFailure(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	m.catalog = func(context.Context, profileSources) (profileSources, error) {
		return profileSources{}, errors.New("private source diagnostic")
	}
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: actionConfiguration(in.Profile)}
	if _, err := m.ProfileAction(ctx, req, "admin", "source-failure"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	var count int
	m.db.QueryRow(`SELECT count(*) FROM profiles`).Scan(&count)
	if count != 1 {
		t.Fatal("failed inventory published profile")
	}
	m.db.QueryRow(`SELECT count(*) FROM idempotency`).Scan(&count)
	if count != 0 {
		t.Fatal("failed inventory published replay")
	}
}

// TestDeleteBudgetRollsBackTombstoneAndIntent enforces a hard bound even when every directory is unrelated.
func TestDeleteBudgetRollsBackTombstoneAndIntent(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	dir := filepath.Join(m.state, "generations")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 4097; n++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("noise-%d", n)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	req := profiles.ActionRequest{ContractVersion: 1, Operation: "delete", ProfileID: in.Profile.ID, ExpectedRevision: 1, ConfirmOwnDataDeletion: true, AcknowledgeTasks: true, ConfirmProfileName: in.Profile.Name}
	if _, err := m.ProfileAction(ctx, req, "admin", "budget"); !errors.Is(err, ErrActionBudget) {
		t.Fatal(err)
	}
	list, err := m.Profiles(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal("budget removed profile", list, err)
	}
	for _, table := range []string{"profile_tombstones", "profile_cleanup", "idempotency"} {
		var n int
		if err = m.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial action", table, n, err)
		}
	}
}

// TestCleanupFairRetryDoesNotStarveLaterGeneration preserves failed entries while progressing new work.
func TestCleanupFairRetryDoesNotStarveLaterGeneration(t *testing.T) {
	m, in, _, _ := nativeFixture(t, "volumes")
	ctx := context.Background()
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, start.Job.ID)
	tx, err := m.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 200; n++ {
		id := fmt.Sprintf("blocked-%d", n)
		if err = os.WriteFile(filepath.Join(m.state, "generations", id), nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(`INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id,attempted_count) VALUES(?,?,?,1)`, "a-blocked", id, "job"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(`INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, in.Profile.ID, *done.Job.ResultSnapshotID, done.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.cleanupProfiles(ctx)
	m.mu.Unlock()
	if _, err = os.Stat(filepath.Join(m.state, "generations", *done.Job.ResultSnapshotID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("blocked older entries starved qualified generation", err)
	}
	var failed int
	m.db.QueryRow(`SELECT count(*) FROM profile_cleanup WHERE profile_id='a-blocked'`).Scan(&failed)
	if failed != 200 {
		t.Fatal("unsafe entries lost")
	}
}
