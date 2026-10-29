package jobs

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRenamePreservesPublishedProvenance proves the live manager uses the canonical store.
func TestRenamePreservesPublishedProvenance(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, run.Job.ID)
	indexPath := filepath.Join(m.state, "generations", *done.Job.ResultSnapshotID, "index.db")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := m.RenameProfile(ctx, in.Profile.ID, 1, "Neuer Name – 東京")
	if err != nil || renamed.ID != in.Profile.ID || renamed.Revision != 1 {
		t.Fatal(renamed, err)
	}
	if _, err = m.RenameProfile(ctx, in.Profile.ID, 2, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	after, err := os.ReadFile(indexPath)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("rename changed generation", err)
	}
	state := m.state
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	list, err := reopened.Profiles(ctx)
	if err != nil || len(list) != 1 || list[0].Profile.Name != renamed.Name || *list[0].ActiveSnapshotID != *done.Job.ResultSnapshotID || *list[0].LatestJobID != done.Job.ID {
		t.Fatal(list, err)
	}
	historical, err := reopened.Get(done.Job.ID)
	if err != nil || historical.Job.CapturedProfile.Name != in.Profile.Name {
		t.Fatal(historical, err)
	}
	next, err := reopened.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	completed := terminal(t, reopened, next.Job.ID)
	if completed.Job.State != "completed" || completed.Job.CapturedProfile.Name != renamed.Name || completed.Job.ProfileRevision != 1 {
		t.Fatal(completed)
	}
}
