package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestSwitchVirtualNAS preserves the actual selected virtual ID across two sealed scans.
func TestSwitchVirtualNAS(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, s.Job.ID)
	ctx := context.Background()
	old, err := m.OpenView(ctx, "admin", in.Profile.ID, "virtual-old", "", false)
	if err != nil {
		t.Fatal(err)
	}
	db, closeDB, err := m.generationDB(old.Lease.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	h, err := index.ReadGenerationHeader(ctx, db, old.Lease.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	nav, err := query.ReadNavigation(ctx, db, h, "nas", nil)
	closeDB()
	if err != nil {
		t.Fatal(err)
	}
	s, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	next := terminal(t, m, s.Job.ID)
	got, err := m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "virtual-new", *next.Job.ResultSnapshotID, nav.RootID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection.NodeID == nil || *got.Selection.NodeID == nav.RootID || got.Selection.Fallback != "none" {
		t.Fatal(got.Selection)
	}
	again, err := m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "virtual-new", *next.Job.ResultSnapshotID, nav.RootID)
	if err != nil || again.Selection.NodeID == nil || *again.Selection.NodeID != *got.Selection.NodeID {
		t.Fatal(again.Selection, err)
	}
	if _, err = m.SwitchView(ctx, "other", in.Profile.ID, old.Lease.ID, "foreign", *next.Job.ResultSnapshotID, nav.RootID); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "forged", *next.Job.ResultSnapshotID, "q-nas-forged"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "virtual-new", *next.Job.ResultSnapshotID, ""); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	// Renewal must not look up the virtual selection in the physical preview table.
	renewed, renewErr := m.OpenView(ctx, "admin", in.Profile.ID, "virtual-new", "", true)
	if renewErr != nil || renewed.Selection.NodeID == nil || *renewed.Selection.NodeID != *got.Selection.NodeID {
		t.Fatal(renewed.Selection, renewErr)
	}
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "foreign-snapshot", *next.Job.ResultSnapshotID, *got.Selection.NodeID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	m.readerMu.Lock()
	expires := m.viewLeases[old.Lease.ID].Expires
	m.viewLeases[old.Lease.ID].Expires = time.Time{}
	m.readerMu.Unlock()
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "expired", *next.Job.ResultSnapshotID, nav.RootID); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
	m.readerMu.Lock()
	m.viewLeases[old.Lease.ID].Expires = expires
	m.readerMu.Unlock()

	if err = m.ReleaseView("admin", in.Profile.ID, old.Lease.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.SwitchView(ctx, "admin", in.Profile.ID, old.Lease.ID, "released", *next.Job.ResultSnapshotID, nav.RootID); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
}
