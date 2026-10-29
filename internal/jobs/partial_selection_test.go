package jobs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestFirstPartialRequiresExplicitSelection keeps the empty default distinct from usable partial metadata.
func TestFirstPartialRequiresExplicitSelection(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+"-missing"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(source+"-missing", source)
	submission, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	partial := terminal(t, m, submission.Job.ID)
	if partial.SnapshotState != "partial" {
		t.Fatal(partial)
	}
	plain, err := m.Snapshot(context.Background(), in.Profile.ID)
	if err != nil || plain.SnapshotID != nil || plain.State != nil {
		t.Fatal("partial became default", plain, err)
	}
	selected, err := m.SnapshotSelection(context.Background(), in.Profile.ID, partial.Job.ID)
	if err != nil || selected.SnapshotID == nil || *selected.SnapshotID != *partial.Job.ResultSnapshotID || selected.State == nil || *selected.State != "partial" || selected.EndedAt == nil || *selected.EndedAt != *partial.SnapshotEndedAt || selected.ScanBoundaryCount != "1" {
		t.Fatal(selected, err)
	}
	if _, err = m.SnapshotSelection(context.Background(), in.Profile.ID, "foreign-job"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign selection", err)
	}
	plain, err = m.Snapshot(context.Background(), in.Profile.ID)
	if err != nil || plain.SnapshotID != nil {
		t.Fatal("read changed default", err)
	}
}

// TestCompleteJobCannotMasqueradeAsExplicitPartial rejects selection ambiguity.
func TestCompleteJobCannotMasqueradeAsExplicitPartial(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	if _, err = m.SnapshotSelection(context.Background(), in.Profile.ID, done.Job.ID); !errors.Is(err, ErrScanResultUnavailable) {
		t.Fatal(err)
	}
}

// TestSnapshotIntervalPrecedesCompletion preserves the capture end across delayed publication.
func TestSnapshotIntervalPrecedesCompletion(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	m.publishHook = func(phase string) error {
		if phase == "before_staged_sync" {
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	view, err := m.Snapshot(context.Background(), in.Profile.ID)
	if err != nil || view.EndedAt == nil || done.SnapshotEndedAt == nil || done.Job.EndedAt == nil {
		t.Fatal(view, done, err)
	}
	capture, err := time.Parse(time.RFC3339Nano, *view.EndedAt)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := time.Parse(time.RFC3339Nano, *done.Job.EndedAt)
	if err != nil {
		t.Fatal(err)
	}
	if *view.EndedAt != *done.SnapshotEndedAt || !completion.After(capture) {
		t.Fatal("capture and terminal times conflated", done)
	}
}
