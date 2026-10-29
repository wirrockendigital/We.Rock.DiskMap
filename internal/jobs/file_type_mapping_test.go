package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestMappingFrozenRevisionAndScanConflict verifies persisted settings and immutable job capture.
func TestMappingFrozenRevisionAndScanConflict(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	initial, err := m.GetFileTypeMapping(ctx)
	if err != nil || initial.Revision != 1 {
		t.Fatal(initial, err)
	}
	next := contracts.FileTypeMapping{Version: 1, Revision: 2, Assignments: []contracts.ExtensionMapping{{Extension: "图像", Category: "image"}}}
	got, err := m.SetFileTypeMapping(ctx, MappingUpdate{ContractVersion: 1, ExpectedRevision: 1, Mapping: next})
	if err != nil || got.Revision != 2 {
		t.Fatal(got, err)
	}
	if _, err = m.SetFileTypeMapping(ctx, MappingUpdate{ContractVersion: 1, ExpectedRevision: 1, Mapping: next}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	sub, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	finished := terminal(t, m, sub.Job.ID)
	if finished.FileTypeMapping.Revision != 2 {
		t.Fatal(finished)
	}
	db, err := index.Open(filepath.Join(m.state, "generations", *finished.Job.ResultSnapshotID, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h, err := index.ReadGenerationHeader(ctx, db, *finished.Job.ResultSnapshotID)
	if err != nil || h.FileTypeMapping.Revision != 2 || h.AggregateVersion != 1 {
		t.Fatal(h, err)
	}
	// The active pointer fixture makes configuration exclusion deterministic, without scheduling races.
	m.mu.Lock()
	m.active = &finished
	m.mu.Unlock()
	next.Revision = 3
	if _, err = m.SetFileTypeMapping(ctx, MappingUpdate{ContractVersion: 1, ExpectedRevision: 2, Mapping: next}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.active = nil
	m.mu.Unlock()
	if _, err = m.SetFileTypeMapping(ctx, MappingUpdate{ContractVersion: 1, ExpectedRevision: 2, Mapping: next}); err != nil {
		t.Fatal(err)
	}
	old, err := m.Get(sub.Job.ID)
	if err != nil || old.FileTypeMapping.Revision != 2 {
		t.Fatal(old, err)
	}
	state := m.state
	m.Close()
	m, err = Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	restored, err := m.GetFileTypeMapping(ctx)
	if err != nil || restored.Revision != 3 {
		t.Fatal(restored, err)
	}
}
