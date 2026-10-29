package jobs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestHierarchyLeaseBoundary proves queries retain the selected stored generation without reading live sources.
func TestHierarchyLeaseBoundary(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	ctx := context.Background()
	view, err := m.OpenView(ctx, "admin", in.Profile.ID, "hierarchy-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	inQuery := HierarchyRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas", Metric: "logical", Depth: 3, ElementBudget: 100}}
	// Removing the isolated source path cannot affect a sealed-index query.
	if err = os.Rename(source, source+"-query-away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(source+"-query-away", source) })
	got, err := m.ReadHierarchy(ctx, "admin", inQuery, false)
	if err != nil || got.(query.Response).SnapshotID != inQuery.Query.SnapshotID {
		t.Fatal(got, err)
	}
	if _, err = m.ReadHierarchy(ctx, "other-admin", inQuery, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign lease", err)
	}
	wrong := inQuery
	wrong.Query.SnapshotID = "other-snapshot"
	if _, err = m.ReadHierarchy(ctx, "admin", wrong, false); !errors.Is(err, query.ErrSnapshot) {
		t.Fatal("snapshot substitution", err)
	}
	// Reserved reads are bounded and protect their generation independently from lease renewal.
	a, err := m.reserveHierarchy(ctx, "admin", inQuery)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.reserveHierarchy(ctx, "admin", inQuery)
	if err != nil {
		a()
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err = m.ReadHierarchy(bounded, "admin", inQuery, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unbounded concurrency", err)
	}
	a()
	b()
	now := time.Now().Add(viewTTL + time.Minute)
	m.viewClock = func() time.Time { return now }
	if _, err = m.ReadHierarchy(ctx, "admin", inQuery, false); !errors.Is(err, ErrViewExpired) {
		t.Fatal("expired lease", err)
	}
}
