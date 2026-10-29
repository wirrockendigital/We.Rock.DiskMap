package jobs

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"os"
	"path/filepath"
	"testing"
)

// Reproduce a repaired permission gap followed by a subtree-only rescan.
func TestScopeCompositionRepairsPermissionGap(t *testing.T) {
	m, in, catalog := bootFixture(t)
	ctx := context.Background()
	share := catalog.Shares[0]
	dir := filepath.Join(share.PhysicalPath, "repaired")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "file"), []byte("hello"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0000); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	j, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	before := terminal(t, m, j.Job.ID)
	if before.SnapshotState != "partial" {
		t.Fatal("expected permission gap", before)
	}
	req := ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}}}
	old, e := m.ScopeObservations(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	config := actionConfiguration(in.Profile)
	config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: "repaired"}
	created, e := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", "review-repair")
	if e != nil {
		t.Fatal(e)
	}
	j, e = m.Start(created.ProfileID, "full")
	if e != nil {
		t.Fatal(e)
	}
	if terminal(t, m, j.Job.ID).SnapshotState != "complete" {
		t.Fatal("repair incomplete")
	}
	got, e := m.ScopeObservations(ctx, req)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("before=%+v after=%+v bytes=%v", old[0], got[0], *got[0].Bytes)
	if got[0].State != "complete" || got[0].ReasonCount != 0 {
		t.Errorf("repaired subtree keeps obsolete coverage gap: state=%s reasons=%+v", got[0].State, got[0].Reasons)
	}
}

// A successful child cannot clear an unreadable ancestor; truncated detail lists retain exact counts.
func TestScopeCompositionPreservesOtherGaps(t *testing.T) {
	for _, gaps := range []int{1, 20} {
		t.Run(fmt.Sprint(gaps), func(t *testing.T) {
			m, in, catalog := bootFixture(t)
			ctx := context.Background()
			share := catalog.Shares[0]
			for i := 0; i < gaps; i++ {
				dir := filepath.Join(share.PhysicalPath, fmt.Sprintf("gap%02d", i))
				os.Mkdir(dir, 0700)
				os.WriteFile(filepath.Join(dir, "file"), []byte("hello"), 0600)
				os.Chmod(dir, 0000)
				defer os.Chmod(dir, 0700)
			}
			start := func(profile string) {
				j, e := m.Start(profile, "full")
				if e != nil {
					t.Fatal(e)
				}
				terminal(t, m, j.Job.ID)
			}
			start(in.Profile.ID)
			os.Chmod(filepath.Join(share.PhysicalPath, "gap00"), 0700)
			config := actionConfiguration(in.Profile)
			config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: "gap00"}
			created, e := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", "repair-one")
			if e != nil {
				t.Fatal(e)
			}
			start(created.ProfileID)
			got, e := m.ScopeObservations(ctx, ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}}})
			if e != nil {
				t.Fatal(e)
			}
			if got[0].ReasonCount != gaps-1 {
				t.Fatalf("remaining gaps: %+v", got[0])
			}
			want := "partial"
			if gaps == 1 {
				want = "complete"
			}
			if got[0].State != want {
				t.Fatal(got[0])
			}
			for _, reason := range got[0].Reasons {
				if reason.RelativePath == "gap00" {
					t.Fatal("repaired reason retained")
				}
			}
		})
	}
}

// A newly unreadable subtree contributes its explanation to an older complete parent observation.
func TestScopeCompositionAddsNewGap(t *testing.T) {
	m, in, catalog := bootFixture(t)
	ctx := context.Background()
	share := catalog.Shares[0]
	dir := filepath.Join(share.PhysicalPath, "later")
	os.Mkdir(dir, 0700)
	os.WriteFile(filepath.Join(dir, "file"), []byte("hello"), 0600)
	j, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, j.Job.ID)
	config := actionConfiguration(in.Profile)
	config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: "later"}
	created, e := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", "later-gap")
	if e != nil {
		t.Fatal(e)
	}
	os.Chmod(dir, 0000)
	defer os.Chmod(dir, 0700)
	j, e = m.Start(created.ProfileID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, j.Job.ID)
	got, e := m.ScopeObservations(ctx, ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}}})
	if e != nil {
		t.Fatal(e)
	}
	if got[0].State != "partial" || got[0].ReasonCount < 1 || len(got[0].Reasons) == 0 || got[0].Reasons[0].RelativePath != "later" {
		t.Fatalf("new gap missing: %+v", got[0])
	}
}

// Scanning one descendant of a repaired ancestor cannot prove the ancestor's other contents complete.
func TestScopeCompositionRetainsAncestorGap(t *testing.T) {
	m, in, catalog := bootFixture(t)
	ctx := context.Background()
	share := catalog.Shares[0]
	parent := filepath.Join(share.PhysicalPath, "parent")
	os.MkdirAll(filepath.Join(parent, "child"), 0700)
	os.WriteFile(filepath.Join(parent, "child", "file"), []byte("hello"), 0600)
	os.WriteFile(filepath.Join(parent, "other"), []byte("unmeasured"), 0600)
	os.Chmod(parent, 0000)
	defer os.Chmod(parent, 0700)
	j, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, j.Job.ID)
	os.Chmod(parent, 0700)
	config := actionConfiguration(in.Profile)
	config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: "parent/child"}
	created, e := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", "ancestor-gap")
	if e != nil {
		t.Fatal(e)
	}
	j, e = m.Start(created.ProfileID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, j.Job.ID)
	got, e := m.ScopeObservations(ctx, ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}, {share.ID, "parent"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range got {
		if v.State != "partial" || v.ReasonCount != 1 || len(v.Reasons) != 1 || v.Reasons[0].RelativePath != "parent" {
			t.Fatalf("ancestor gap erased: %+v", v)
		}
		if v.Bytes == nil || *v.Bytes == "0" {
			t.Fatal("new readable descendant bytes missing")
		}
	}
}
