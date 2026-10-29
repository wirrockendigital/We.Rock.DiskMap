package jobs

import (
	"context"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Explicit start profiles override foreign remembered views without writes or automatic scans.
func TestViewRestorePreferredProfilePrecedence(t *testing.T) {
	m, in, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	scanRetention(t, m, in.Profile.ID)
	initial, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "initial-a", 0})
	if err != nil || initial.ResolvedView == nil {
		t.Fatal(initial, err)
	}
	view := *initial.ResolvedView
	view.Map, view.Metric, view.Sort, view.Direction = "radial", "allocated", "size", "desc"
	for _, node := range initial.Reader.View.Nodes {
		if node.Kind == "file" {
			id := node.ID
			view.SelectedNodeID = &id
			break
		}
	}
	if view.SelectedNodeID == nil {
		t.Fatal("missing fixture selection")
	}
	pref, err := m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "remember-a")
	if err != nil {
		t.Fatal(err)
	}
	other := in
	other.Profile.ID = "explicit-start-b"
	if err = m.ImportProfile(other); err != nil {
		t.Fatal(err)
	}
	// A chosen profile with no snapshot stays empty rather than borrowing A's data.
	pref, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: pref.Revision, Group: "preferredProfile", PreferredProfile: &PreferredProfileChange{ProfileID: &other.Profile.ID}}, "admin", "choose-b")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "empty-b", pref.Revision})
	if err != nil || empty.ProfileID == nil || *empty.ProfileID != other.Profile.ID || empty.Reader != nil || !reflect.DeepEqual(empty.Reasons, []string{"NO_SNAPSHOT"}) {
		t.Fatal(empty, err)
	}
	snapshot := scanRetention(t, m, other.Profile.ID)
	fresh, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "fresh-b", pref.Revision})
	if err != nil || fresh.ResolvedView == nil {
		t.Fatal(fresh, err)
	}
	if fresh.ResolvedView.ProfileID != other.Profile.ID || fresh.ResolvedView.SnapshotID != snapshot || fresh.ResolvedView.SelectedNodeID != nil || fresh.ResolvedView.Map != pref.Map || fresh.ResolvedView.Metric != "logical" || fresh.ResolvedView.Sort != "name" {
		t.Fatal("foreign view leaked", fresh.ResolvedView)
	}
	// Both an explicit matching profile and automatic mode preserve the entire remembered view.
	for _, test := range []struct {
		name    string
		profile *string
	}{{"matching-a", &in.Profile.ID}, {"automatic-a", nil}} {
		pref, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: pref.Revision, Group: "preferredProfile", PreferredProfile: &PreferredProfileChange{ProfileID: test.profile}}, "admin", test.name)
		if err != nil {
			t.Fatal(err)
		}
		got, e := m.RestoreView(ctx, "admin", RestoreRequest{1, test.name, pref.Revision})
		if e != nil || !reflect.DeepEqual(got.ResolvedView, &view) {
			t.Fatal(test.name, got, e)
		}
	}
	// A deleted explicit profile keeps the existing explained catalogue fallback.
	pref, err = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: pref.Revision, Group: "preferredProfile", PreferredProfile: &PreferredProfileChange{ProfileID: &other.Profile.ID}}, "admin", "choose-before-delete")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec("DELETE FROM profiles WHERE id=?", other.Profile.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.RestoreView(ctx, "admin", RestoreRequest{1, "deleted-b", pref.Revision})
	if err != nil || !reflect.DeepEqual(got.ResolvedView, &view) || !reflect.DeepEqual(got.Reasons, []string{"PROFILE_UNAVAILABLE"}) {
		t.Fatal(got, err)
	}
	after, err := m.Preferences(ctx, "admin")
	if err != nil || !reflect.DeepEqual(after, pref) {
		t.Fatal("restore rewrote preferences", after, err)
	}
	var jobs int
	if err = m.db.QueryRow("SELECT count(*) FROM jobs").Scan(&jobs); err != nil || jobs != 2 {
		t.Fatal("restore started work", jobs, err)
	}
}
