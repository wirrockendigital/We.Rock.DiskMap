package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Search uses a pinned stored index even when its original source is unavailable.
func TestSearchLeaseIsolationAndSharedBudget(t *testing.T) {
	m, in, source := fixture(t)
	if e := os.WriteFile(filepath.Join(source, "second"), []byte("xyz"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	job, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, job.Job.ID)
	if done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	ctx := context.Background()
	view, e := m.OpenView(ctx, "admin", in.Profile.ID, "search-view", "", false)
	if e != nil {
		t.Fatal(e)
	}
	req := SearchRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, Query: query.SearchRequest{SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas", Scope: "recursive", Field: "both", Metric: "logical", Sort: "name", Direction: "asc", Limit: 50}}
	otherProfile := in
	otherProfile.Profile.ID = "other-profile"
	if e = m.ImportProfile(otherProfile); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(source, source+"-offline-search"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Rename(source+"-offline-search", source) })
	page, e := m.ReadSearch(ctx, "admin", req)
	if e != nil || page.SnapshotID != req.Query.SnapshotID || len(page.Items) == 0 {
		t.Fatal(page, e)
	}
	if _, e = m.ReadSearch(ctx, "other", req); !errors.Is(e, ErrNotFound) {
		t.Fatal("principal isolation", e)
	}
	wrong := req
	wrong.Query.SnapshotID = "another"
	if _, e = m.ReadSearch(ctx, "admin", wrong); !errors.Is(e, query.ErrSnapshot) {
		t.Fatal("snapshot isolation", e)
	}
	wrong = req
	wrong.ProfileID = "absent-profile"
	if _, e = m.ReadSearch(ctx, "admin", wrong); !errors.Is(e, ErrProfileMissing) {
		t.Fatal("profile isolation", e)
	}
	wrong = req
	wrong.ProfileID = "other-profile"
	if _, e = m.ReadSearch(ctx, "admin", wrong); !errors.Is(e, ErrNotFound) {
		t.Fatal("existing foreign profile", e)
	}
	first := req
	first.Query.Limit = 1
	firstPage, e := m.ReadSearch(ctx, "admin", first)
	if e != nil || firstPage.NextCursor == "" {
		t.Fatal(firstPage, e)
	}
	first.Cursor = firstPage.NextCursor
	first.Query.Sort = "size"
	if _, e = m.ReadSearch(ctx, "admin", first); !errors.Is(e, query.ErrSearch) {
		t.Fatal("cursor changed selector", e)
	}
	for _, cursor := range []string{"invalid!", strings.Repeat("x", query.MaxSearchCursorBytes+1)} {
		wrong = req
		wrong.Cursor = cursor
		if _, e = m.ReadSearch(ctx, "admin", wrong); e == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	wrong = req
	wrong.Query.Sort = "private-path"
	if _, e = m.ReadSearch(ctx, "admin", wrong); e == nil {
		t.Fatal("invalid selector accepted")
	}
	binding := HierarchyRequest{ContractVersion: 1, ProfileID: req.ProfileID, ViewID: req.ViewID, Query: query.Request{SnapshotID: req.Query.SnapshotID, RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 1}}
	a, e := m.reserveHierarchy(ctx, "admin", binding)
	if e != nil {
		t.Fatal(e)
	}
	b, e := m.reserveHierarchy(ctx, "admin", binding)
	if e != nil {
		a()
		t.Fatal(e)
	}
	// Shared slots now wait within the original caller deadline instead of rejecting immediately.
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, e = m.ReadSearch(bounded, "admin", req)
	cancel()
	a()
	b()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("shared two-slot budget", e)
	}
	m.viewClock = func() time.Time { return time.Now().Add(viewTTL + time.Minute) }
	if _, e = m.ReadSearch(ctx, "admin", req); !errors.Is(e, ErrViewExpired) {
		t.Fatal("expired lease", e)
	}
}
