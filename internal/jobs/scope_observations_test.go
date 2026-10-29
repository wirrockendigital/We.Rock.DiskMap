package jobs

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"math/big"
	"os"
	"path/filepath"
	"testing"
)

// A readable recycle folder retains its bytes when selected; parent totals still separate recycling.
func TestScopeObservationsRecycleFolderSize(t *testing.T) {
	m, in, catalog := bootFixture(t)
	root := catalog.Shares[0].PhysicalPath
	if err := os.MkdirAll(filepath.Join(root, "#recycle", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "#recycle", "nested", "file"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	j, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, j.Job.ID)
	got, err := m.ScopeObservations(context.Background(), ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{catalog.Shares[0].ID, "#recycle"}, {catalog.Shares[0].ID, "#recycle/nested"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range got {
		if value.Bytes == nil || *value.Bytes != "5" || value.RecycleBytes == nil || *value.RecycleBytes != "5" {
			t.Fatalf("lost recycle bytes: %+v", value)
		}
	}
}

// A scope read must retain sibling sizes without borrowing the currently selected reader.
func TestScopeObservationsIndependentOfView(t *testing.T) {
	m, in, catalog := bootFixture(t)
	source := catalog.Shares[0].PhysicalPath
	if err := os.MkdirAll(filepath.Join(source, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "child", "file"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	job, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	result := terminal(t, m, job.Job.ID)
	if result.SnapshotState != "complete" {
		t.Fatal(result)
	}
	req := ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{ShareID: catalog.Shares[0].ID, Path: "."}, {ShareID: catalog.Shares[0].ID, Path: "child"}}}
	got, err := m.ScopeObservations(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Bytes == nil || *got[1].Bytes != "5" || got[1].State != "complete" || got[0].EndedAt == nil {
		t.Fatalf("unexpected observations: %+v", got)
	}
	// A missing path is unknown, never reported as a measured empty directory.
	req.Scopes[1].Path = "absent"
	got, err = m.ScopeObservations(context.Background(), req)
	if err != nil || got[1].Bytes != nil {
		t.Fatal(got, err)
	}
	req.Scopes[0].Path = "../outside"
	if _, err = m.ScopeObservations(context.Background(), req); err == nil {
		t.Fatal("traversal accepted")
	}
}

// A newer folder scan replaces the corresponding old bytes, including nested overlapping scans.
func TestScopeObservationsComposeNewerSubscans(t *testing.T) {
	m, in, catalog := bootFixture(t)
	ctx := context.Background()
	share := catalog.Shares[0]
	source := share.PhysicalPath
	if e := os.MkdirAll(filepath.Join(source, "a", "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	for name, value := range map[string]string{"a/file": "12345", "a/nested/file": "123"} {
		if e := os.WriteFile(filepath.Join(source, name), []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
	}
	start := func(profile string) {
		j, e := m.Start(profile, "full")
		if e != nil {
			t.Fatal(e)
		}
		if o := terminal(t, m, j.Job.ID); o.SnapshotState != "complete" {
			t.Fatal(o)
		}
	}
	start(in.Profile.ID)
	req := ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}}}
	before, e := m.ScopeObservations(ctx, req)
	if e != nil || before[0].Bytes == nil {
		t.Fatal(before, e)
	}
	expected, _ := new(big.Int).SetString(string(*before[0].Bytes), 10)
	for i, step := range []struct{ path, value string }{{"a", "1234567"}, {"a/nested", "12345678901"}} {
		if e := os.WriteFile(filepath.Join(source, step.path, "file"), []byte(step.value), 0600); e != nil {
			t.Fatal(e)
		}
		config := actionConfiguration(in.Profile)
		config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: step.path}
		created, e := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", fmt.Sprintf("compose-%d", i))
		if e != nil {
			t.Fatal(e)
		}
		start(created.ProfileID)
	}
	expected.Add(expected, big.NewInt(10))
	after, e := m.ScopeObservations(ctx, req)
	if e != nil || after[0].Bytes == nil || string(*after[0].Bytes) != expected.String() {
		t.Fatal("overlapping totals", before, after, e)
	}
}

// Category flags count each boundary once and do not confuse ordinary dotfiles with system files.
func TestScopeObservationUnreadableCategories(t *testing.T) {
	for _, tc := range []struct {
		path            string
		recycle, system bool
	}{{"#recycle/nested/#recycle", true, false}, {"folder/@eaDir/file", false, true}, {"#recycle/.DS_Store", true, true}, {"folder/.SMBtest", false, true}, {"folder/.ordinary", false, false}} {
		var out ScopeObservation
		markUnreadableCategory(&out, tc.path)
		if out.UnreadableRecycle != tc.recycle || out.UnreadableSystem != tc.system || out.unreadableRecycleCount > 1 || out.unreadableSystemCount > 1 {
			t.Fatalf("%s: %+v", tc.path, out)
		}
	}
}
