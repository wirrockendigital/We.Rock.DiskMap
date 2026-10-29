package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Restore acquires a fresh principal-bound lease and preserves it across later publications.
func TestViewRestoreQualifiedAndPinned(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	first := terminal(t, m, start.Job.ID)
	out, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "restore-one", 0})
	if err != nil || out.Reader == nil || out.Navigation == nil || out.ResolvedView.SnapshotID != *first.Job.ResultSnapshotID {
		t.Fatal(out, err)
	}
	if out.ResolvedView.RootID == "nas" || len(out.Navigation.Breadcrumb) != 1 {
		t.Fatal(out.Navigation)
	}
	var selected string
	for _, n := range out.Reader.View.Nodes {
		if n.Kind == "file" {
			selected = n.ID
			break
		}
	}
	if selected == "" {
		t.Fatal("missing fixture file")
	}
	view := *out.ResolvedView
	view.SelectedNodeID = &selected
	pref, err := m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: 0, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "remember")
	if err != nil {
		t.Fatal(err)
	}
	restore, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "remembered", pref.Revision})
	if err != nil || restore.Navigation.SelectedNodeID == nil {
		t.Fatal(restore, err)
	}
	// Removing the source does not invalidate immutable metadata or authorize new source reads.
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	nav, err := m.ReadNavigation(ctx, "admin", NavigationRequest{1, in.Profile.ID, "remembered", view.SnapshotID, view.RootID, &selected})
	if err != nil || nav.SelectedNodeID == nil {
		t.Fatal(nav, err)
	}
	if err = os.Rename(source+"-offline", source); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadNavigation(ctx, "foreign", NavigationRequest{1, in.Profile.ID, "remembered", view.SnapshotID, view.RootID, nil}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = m.ReadNavigation(ctx, "admin", NavigationRequest{1, in.Profile.ID, "remembered", "foreign", view.RootID, nil}); err == nil {
		t.Fatal("snapshot bypass")
	}
	if _, err = m.RestoreView(ctx, "admin", RestoreRequest{1, "stale", 0}); !errors.Is(err, ErrPreferenceConflict) {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "new"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	start, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, start.Job.ID)
	retry, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "remembered", pref.Revision})
	if err != nil || retry.ResolvedView.SnapshotID != view.SnapshotID {
		t.Fatal(retry, err)
	}
	if err = m.ReleaseView("admin", in.Profile.ID, "remembered"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RestoreView(ctx, "admin", RestoreRequest{1, "remembered", pref.Revision}); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
}

// Invalid remembered identities receive explicit fallbacks without changing the preference row.
func TestViewRestoreFallbacksAndEmpty(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	empty, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "empty", 0})
	if err != nil || empty.ProfileID != nil || len(empty.Reasons) != 1 {
		t.Fatal(empty, err)
	}
	if err = m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	empty, err = m.RestoreView(ctx, "admin", RestoreRequest{1, "empty-profile", 0})
	if err != nil || empty.ProfileID == nil || empty.Reader != nil {
		t.Fatal(empty, err)
	}
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, start.Job.ID)
	view := contracts.PreferenceView{ProfileID: in.Profile.ID, SnapshotID: *done.Job.ResultSnapshotID, RootID: "missing-node", Map: "radial", Metric: "allocated", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "size", Direction: "desc"}
	missing := "missing-selection"
	view.SelectedNodeID = &missing
	_, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "save")
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "fallback", 1})
	if err != nil || out.Navigation.SelectedNodeID != nil || len(out.Reasons) != 2 || out.ResolvedView.Map != "radial" {
		t.Fatal(out, err)
	}
	before, err := m.Preferences(ctx, "admin")
	if err != nil || before.LastView.RootID != "missing-node" {
		t.Fatal(before, err)
	}
	// A removed historical generation does not transfer its opaque node identities into the active generation.
	view.SnapshotID = "deleted-generation"
	_, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: 1, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "save-deleted")
	if err != nil {
		t.Fatal(err)
	}
	out, err = m.RestoreView(ctx, "admin", RestoreRequest{1, "generation-fallback", 2})
	if err != nil || out.ResolvedView.SnapshotID != *done.Job.ResultSnapshotID || out.Navigation.SelectedNodeID != nil || out.Reasons[0] != "SNAPSHOT_UNAVAILABLE" {
		t.Fatal(out, err)
	}
	// Expiry is checked independently of successful earlier acquisition.
	now := time.Now()
	m.viewClock = func() time.Time { return now.Add(5 * time.Minute) }
	if _, err = m.ReadNavigation(ctx, "admin", NavigationRequest{1, in.Profile.ID, "generation-fallback", *done.Job.ResultSnapshotID, "nas", nil}); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
}

// Restart reopens the same qualified generation with a new lease; deleted profiles fall back without scanning.
func TestViewRestoreRestartAndDeletedProfile(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, start.Job.ID)
	initial, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "before-restart", 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "lastView", View: &PreferenceViewChange{Value: initial.ResolvedView}}, "admin", "save"); err != nil {
		t.Fatal(err)
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
	restored, err := reopened.RestoreView(ctx, "admin", RestoreRequest{1, "after-restart", 1})
	if err != nil || restored.Reader.Lease.ID != "after-restart" || restored.ResolvedView.SnapshotID != initial.ResolvedView.SnapshotID {
		t.Fatal(restored, err)
	}
	// The fixture deletes only the profile record to reproduce a stale remembered reference.
	if _, err = reopened.db.Exec(`DELETE FROM active_generations WHERE profile_id=?`, in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.db.Exec(`DELETE FROM profiles WHERE id=?`, in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	absent, err := reopened.RestoreView(ctx, "admin", RestoreRequest{1, "deleted-profile", 1})
	if err != nil || absent.ProfileID != nil || len(absent.Reasons) != 2 || absent.Reasons[0] != "PROFILE_UNAVAILABLE" {
		t.Fatal(absent, err)
	}
	next := in
	next.Profile.ID = "replacement-profile"
	if err = reopened.ImportProfile(next); err != nil {
		t.Fatal(err)
	}
	fallback, err := reopened.RestoreView(ctx, "admin", RestoreRequest{1, "valid-fallback", 1})
	if err != nil || fallback.ProfileID == nil || *fallback.ProfileID != next.Profile.ID || fallback.ResolvedView != nil {
		t.Fatal(fallback, err)
	}
	// A corrupt preference row remains an error, never a silently repaired default.
	if _, err = reopened.db.Exec(`UPDATE user_preferences SET value_json='{"revision":1}' WHERE principal='admin'`); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.RestoreView(ctx, "admin", RestoreRequest{1, "corrupt-pref", 1}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
