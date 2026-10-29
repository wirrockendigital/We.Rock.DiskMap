package jobs

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// An expired request must not remain queued behind unrelated lifecycle maintenance.
func TestQueryReservationCancellationDuringMaintenance(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.reserveHierarchy(ctx, "admin", HierarchyRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: "held-view", Query: query.Request{SnapshotID: "snapshot", RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 50}})
		done <- err
	}()
	select {
	case err := <-done:
		m.mu.Unlock()
		if err != context.DeadlineExceeded {
			t.Fatalf("got %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		m.mu.Unlock()
		<-done
		t.Fatal("expired query remained blocked by maintenance lock")
	}
}

// A held maintenance probe cannot block an authorized read of the same immutable generation.
func TestRetentionQualificationDoesNotHoldQueryLock(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	view, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "contention-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.mu.Lock()
	m.retentionProbe = func() { once.Do(func() { close(entered); <-release }) }
	m.mu.Unlock()
	go func() {
		m.mu.Lock()
		m.retainGenerationRound(context.Background(), true)
		m.mu.Unlock()
		close(finished)
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = m.ReadHierarchy(ctx, "admin", HierarchyRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: snapshot, RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 50}}, false)
	close(release)
	<-finished
	m.mu.Lock()
	m.retentionProbe = nil
	m.mu.Unlock()
	if err != nil {
		t.Fatalf("maintenance blocked hierarchy: %v", err)
	}
}

// A policy changed during unlocked qualification invalidates the entire old deletion decision.
func TestRetentionRechecksPolicyAfterQualification(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRetention(context.Background(), RetentionRequest{1, 30}, "admin", "keep-thirty"); err != nil {
		t.Fatal(err)
	}
	first := scanRetention(t, m, in.Profile.ID)
	second := scanRetention(t, m, in.Profile.ID)
	m.mu.Lock()
	if _, err := m.db.Exec(`UPDATE retention_settings SET complete_count=1`); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.retentionProbe = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		// This models a committed concurrent setting change, not a runtime fault hook.
		if _, err := m.db.Exec(`UPDATE retention_settings SET complete_count=30`); err != nil {
			t.Error(err)
		}
	}
	m.retainGenerationRound(context.Background(), true)
	m.retentionProbe = nil
	m.mu.Unlock()
	if !generationExists(m, first) || !generationExists(m, second) {
		t.Fatal("stale retention policy removed a protected generation")
	}
}

// Invalidating the active member during qualification must preserve the older usable fallback.
func TestRetentionRechecksIntegrityAfterQualification(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRetention(context.Background(), RetentionRequest{1, 30}, "admin", "keep-for-check"); err != nil {
		t.Fatal(err)
	}
	first := scanRetention(t, m, in.Profile.ID)
	active := scanRetention(t, m, in.Profile.ID)
	m.mu.Lock()
	if _, err := m.db.Exec(`UPDATE retention_settings SET complete_count=1`); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.retentionProbe = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		// A committed check invalidates prior health while keeping the active pointer unchanged.
		_, err := m.db.Exec(`UPDATE integrity_generations SET quality='unverified' WHERE snapshot_id=?`, active)
		if err != nil {
			t.Error(err)
		}
		m.integrityStates.Store(active, integrityGuard{quality: "unverified"})
	}
	m.retainGenerationRound(context.Background(), true)
	m.retentionProbe = nil
	m.mu.Unlock()
	if !generationExists(m, first) || !generationExists(m, active) {
		t.Fatal("changed integrity discarded last usable fallback")
	}
}

// A disappeared protected file cannot count toward the retention promise.
func TestRetentionRechecksMissingProtectedGeneration(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRetention(context.Background(), RetentionRequest{1, 30}, "admin", "keep-for-check"); err != nil {
		t.Fatal(err)
	}
	first := scanRetention(t, m, in.Profile.ID)
	active := scanRetention(t, m, in.Profile.ID)
	m.mu.Lock()
	if _, err := m.db.Exec(`UPDATE retention_settings SET complete_count=1`); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.retentionProbe = func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		// Moving only this test's active index models disappearance after qualification.
		path := filepath.Join(m.state, "generations", active, "index.db")
		if err := os.Rename(path, path+".away"); err != nil {
			t.Error(err)
		}
		t.Cleanup(func() { _ = os.Rename(path+".away", path) })
	}
	m.retainGenerationRound(context.Background(), true)
	m.retentionProbe = nil
	m.mu.Unlock()
	if !generationExists(m, first) {
		t.Fatal("missing protected generation discarded last usable fallback")
	}
}
