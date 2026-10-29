package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// TestPartialJobBoundariesDoNotReplaceActiveSnapshot proves read access to a sealed partial result.
func TestPartialJobBoundariesDoNotReplaceActiveSnapshot(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	first, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	complete := terminal(t, m, first.Job.ID)
	// A removed source produces a real partial generation without changing active completeness.
	if err := os.Rename(source, source+"-away"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(source+"-away", source)
	next, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	partial := terminal(t, m, next.Job.ID)
	if partial.SnapshotState != "partial" {
		t.Fatal(partial)
	}
	got, err := m.ScanBoundaries(context.Background(), in.Profile.ID, next.Job.ID)
	if err != nil || got.State != "partial" || got.JobID != next.Job.ID || got.SnapshotID != *partial.Job.ResultSnapshotID || got.Count != "1" || len(got.Boundaries) != 1 || got.Boundaries[0].ReasonCode != "SOURCE_MISSING" {
		t.Fatal(got, err)
	}
	active, err := m.Snapshot(context.Background(), in.Profile.ID)
	if err != nil || active.SnapshotID == nil || *active.SnapshotID != *complete.Job.ResultSnapshotID {
		t.Fatal(active, err)
	}
	if _, err := m.ScanBoundaries(context.Background(), "other", next.Job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// A job without a successful terminal result must not expose even an existing generation.
	partial.Job.State = "failed"
	raw, _ := json.Marshal(partial)
	if _, err := m.db.Exec(`UPDATE jobs SET outcome_json=? WHERE id=?`, string(raw), next.Job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ScanBoundaries(context.Background(), in.Profile.ID, next.Job.ID); !errors.Is(err, ErrScanResultUnavailable) {
		t.Fatal(err)
	}
}
