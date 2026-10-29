package jobs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestReaderPinsPublicationAndMapsRemovedSelection proves a live lease never follows the mutable default.
func TestReaderPinsPublicationAndMapsRemovedSelection(t *testing.T) {
	m, in, source := fixture(t)
	if err := os.Mkdir(filepath.Join(source, "folder"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "folder", "removed"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	first := terminal(t, m, s.Job.ID)
	ctx := context.Background()
	old, err := m.OpenView(ctx, "admin", in.Profile.ID, "view-one", "", false)
	if err != nil {
		t.Fatal(err)
	}
	var selected string
	for _, n := range old.View.Nodes {
		if n.OriginalName == "removed" {
			selected = n.ID
		}
	}
	if selected == "" {
		t.Fatal("fixture missing")
	}
	if err = os.Remove(filepath.Join(source, "folder", "removed")); err != nil {
		t.Fatal(err)
	}
	s, err = m.Start(in.Profile.ID, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	next := terminal(t, m, s.Job.ID)
	pinned, err := m.OpenView(ctx, "admin", in.Profile.ID, "view-one", "", true)
	if err != nil || *pinned.View.SnapshotID != *first.Job.ResultSnapshotID || pinned.NewCompleteSnapshotID == nil || *pinned.NewCompleteSnapshotID != *next.Job.ResultSnapshotID {
		t.Fatal(pinned, err)
	}
	adopted, err := m.SwitchView(ctx, "admin", in.Profile.ID, "view-one", "view-two", *next.Job.ResultSnapshotID, selected)
	if err != nil || adopted.Selection.Fallback != "ancestor" || adopted.Selection.NodeID == nil {
		t.Fatal(adopted, err)
	}
	for _, n := range adopted.View.Nodes {
		if n.ID == *adopted.Selection.NodeID && n.OriginalName != "folder" {
			t.Fatal("wrong parent", n)
		}
	}
	if _, err = m.OpenView(ctx, "other-admin", in.Profile.ID, "view-one", "", true); !errors.Is(err, ErrNotFound) {
		t.Fatal("owner bypass", err)
	}
	if err = m.ReleaseView("other-admin", in.Profile.ID, "view-one"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign release", err)
	}
}

// TestReaderExpiryLimitsAndCleanup uses a deterministic clock, never sleeps or fills real storage.
func TestReaderExpiryLimitsAndCleanup(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	now := time.Now()
	m.viewClock = func() time.Time { return now }
	ctx := context.Background()
	for n := 0; n < maxPrincipalViews; n++ {
		_, err = m.OpenView(ctx, "admin", in.Profile.ID, fmt.Sprintf("v%d", n), "", false)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "v0", "", false); err != nil {
		t.Fatal("retry consumed quota", err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "overflow", "", false); !errors.Is(err, ErrViewLimit) {
		t.Fatal(err)
	}
	if err = m.cleanupGeneration(ctx, in.Profile.ID, *done.Job.ResultSnapshotID, done.Job.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("live lease deleted", err)
	}
	now = now.Add(viewTTL + time.Nanosecond)
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "v0", "", true); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "v0", "", false); !errors.Is(err, ErrViewExpired) {
		t.Fatal("expired reacquired silently", err)
	}
	if err = m.ReleaseView("admin", in.Profile.ID, "v0"); err != nil {
		t.Fatal(err)
	}
	if err = m.ReleaseView("admin", in.Profile.ID, "v0"); err != nil {
		t.Fatal(err)
	}
}

// TestPartialReaderIsNotReplacedByComplete keeps explicit partial provenance pinned during recovery.
func TestPartialReaderIsNotReplacedByComplete(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+"-away"); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	partial := terminal(t, m, s.Job.ID)
	first, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "partial-view", partial.Job.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.OpenView(context.Background(), "admin", in.Profile.ID, "partial-view", partial.Job.ID, false); err != nil {
		t.Fatal("partial retry failed", err)
	}
	if err = os.Rename(source+"-away", source); err != nil {
		t.Fatal(err)
	}
	s, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	complete := terminal(t, m, s.Job.ID)
	got, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "partial-view", "", true)
	if err != nil || *got.View.SnapshotID != *first.View.SnapshotID || *got.View.State != "partial" || got.NewCompleteSnapshotID == nil || *got.NewCompleteSnapshotID != *complete.Job.ResultSnapshotID {
		t.Fatal(got, err)
	}
}

// TestReaderNullDefaultAndSwitchRetry exercises first acquisition without an existing complete result.
func TestReaderNullDefaultAndSwitchRetry(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	empty, err := m.OpenView(ctx, "admin", in.Profile.ID, "empty", "", false)
	if err != nil || empty.Lease != nil || empty.View.SnapshotID != nil {
		t.Fatal(empty, err)
	}
	again, err := m.OpenView(ctx, "admin", in.Profile.ID, "empty", "", false)
	if err != nil || again.Lease != nil {
		t.Fatal(again, err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "empty", "", true); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, s.Job.ID)
	old, err := m.OpenView(ctx, "admin", in.Profile.ID, "original", "", false)
	if err != nil {
		t.Fatal(err)
	}
	s, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	next := terminal(t, m, s.Job.ID)
	a, err := m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "adopted", *next.Job.ResultSnapshotID, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "adopted", *next.Job.ResultSnapshotID, "")
	if err != nil || a.Lease.ID != b.Lease.ID || a.Lease.SnapshotID != b.Lease.SnapshotID {
		t.Fatal(b, err)
	}
	// A changed selection cannot mutate the response of an uncertain switch retry.
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "adopted", *next.Job.ResultSnapshotID, old.View.Nodes[0].ID); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed switch intent: %v", err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "adopted", "", true); err != nil {
		t.Fatal(err)
	}
	if err = m.ReleaseView("admin", in.Profile.ID, old.Lease.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.ReleaseView("admin", in.Profile.ID, old.Lease.ID); err != nil {
		t.Fatal(err)
	}
}

// TestReaderConcurrentPublishCleanup keeps a live old view readable while a new complete pointer commits.
func TestReaderConcurrentPublishCleanup(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	old := terminal(t, m, s.Job.ID)
	ctx := context.Background()
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "concurrent", "", false); err != nil {
		t.Fatal(err)
	}
	failures := make(chan error, 1)
	go func() {
		for n := 0; n < 20; n++ {
			v, e := m.OpenView(ctx, "admin", in.Profile.ID, "concurrent", "", true)
			if e != nil {
				failures <- e
				return
			}
			if v.View.SnapshotID == nil || *v.View.SnapshotID != *old.Job.ResultSnapshotID {
				failures <- fmt.Errorf("reader switched generation")
				return
			}
			if e = m.cleanupGeneration(ctx, in.Profile.ID, *old.Job.ResultSnapshotID, old.Job.ID, ""); !errors.Is(e, ErrConflict) {
				failures <- fmt.Errorf("cleanup crossed reader: %v", e)
				return
			}
		}
		failures <- nil
	}()
	s, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	next := terminal(t, m, s.Job.ID)
	if err = <-failures; err != nil {
		t.Fatal(err)
	}
	if *next.Job.ResultSnapshotID == *old.Job.ResultSnapshotID {
		t.Fatal("no new generation")
	}
	if err = m.ReleaseView("admin", in.Profile.ID, "concurrent"); err != nil {
		t.Fatal(err)
	}
	if err = m.cleanupGeneration(ctx, in.Profile.ID, *old.Job.ResultSnapshotID, old.Job.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "concurrent", "", true); !errors.Is(err, ErrViewExpired) {
		t.Fatal("deleted selection silently replaced", err)
	}
}

// TestLiveRenewedLeaseSurvivesTombstoneCollection protects a view open longer than the retry retention window.
func TestLiveRenewedLeaseSurvivesTombstoneCollection(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	now := time.Now()
	m.viewClock = func() time.Time { return now }
	ctx := context.Background()
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "long-lived", "", false); err != nil {
		t.Fatal(err)
	}
	// Renew through every lease interval instead of bypassing the expiry rule in test state.
	for n := 0; n < 1441; n++ {
		now = now.Add(time.Minute)
		if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "long-lived", "", true); err != nil {
			t.Fatal(n, err)
		}
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "other-view", "", false); err != nil {
		t.Fatal(err)
	}
	if err = m.cleanupGeneration(ctx, in.Profile.ID, *done.Job.ResultSnapshotID, done.Job.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("active long-lived lease pruned", err)
	}
	if _, err = m.OpenView(ctx, "admin", in.Profile.ID, "long-lived", "", true); err != nil {
		t.Fatal(err)
	}
}
