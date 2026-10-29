package jobs

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"os"
	"path/filepath"
	"testing"
)

// A public folder profile scans only its bound subtree and preserves that selection on rescan.
func TestFolderProfileCreateAndRescan(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	share := filepath.Join(source, "share")
	relative := `Grüße % # " ' Ordner`
	child := filepath.Join(share, relative)
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "inside"), []byte("five!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "second"), []byte("12345678901234567890"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "outside"), []byte("unselected-content"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Shares = []contracts.Share{{ID: "share-a", Name: "Share", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}}
	configuration := actionConfiguration(in.Profile)
	configuration.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{"share-a"}, RelativePath: relative}
	created, err := m.ProfileAction(context.Background(), profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: configuration}, "admin", "folder-create")
	if err != nil {
		t.Fatal(err)
	}
	configuration.Exclusions = []contracts.Exclusion{{Kind: "pattern", Value: "*.unused"}}
	updated, err := m.ProfileAction(context.Background(), profiles.ActionRequest{ContractVersion: 1, Operation: "update", ProfileID: created.ProfileID, ExpectedRevision: 1, Configuration: configuration}, "admin", "folder-update")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		job, err := m.Start(updated.ProfileID, "full")
		if err != nil {
			t.Fatal(err)
		}
		out := terminal(t, m, job.Job.ID)
		if out.SnapshotState != "complete" || out.Job.BytesObserved != "25" || len(out.ScopeManifest.Roots) != 1 || out.ScopeManifest.Roots[0].PhysicalPath != child || out.ScopeManifest.Roots[0].RelativePath != relative {
			t.Fatalf("wrong subtree result: %+v", out)
		}
		// Completed folder generations must remain readable through the public lease and query boundary.
		ctx := context.Background()
		view, err := m.OpenView(ctx, "admin", updated.ProfileID, fmt.Sprintf("folder-reader-%d", i), "", false)
		if err != nil {
			t.Fatalf("folder reader: %v", err)
		}
		snapshot := *out.Job.ResultSnapshotID
		db, err := index.Open(filepath.Join(m.state, "generations", snapshot, "index.db"))
		if err != nil {
			t.Fatal(err)
		}
		rootRecord, err := index.FindNode(ctx, db, "share-a", []byte("."))
		db.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, root := range []string{"nas", rootRecord.Node.ID} {
			if _, err = m.ReadNavigation(ctx, "admin", NavigationRequest{1, updated.ProfileID, view.Lease.ID, snapshot, root, nil}); err != nil {
				t.Fatalf("folder navigation: %v", err)
			}
			got, err := m.ReadHierarchy(ctx, "admin", HierarchyRequest{ContractVersion: 1, ProfileID: updated.ProfileID, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: snapshot, RootID: root, Metric: "logical", Depth: 3, ElementBudget: 100}}, false)
			if err != nil {
				t.Fatalf("folder hierarchy: %v", err)
			}
			if got.(query.Response).SnapshotID != snapshot {
				t.Fatal("folder hierarchy changed snapshot")
			}

		}
	}
	if err = os.Rename(child, child+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(child+"-old", child); err != nil {
		t.Fatal(err)
	}
	job, err := m.Start(updated.ProfileID, "full")
	if err == nil {
		out := terminal(t, m, job.Job.ID)
		if out.SnapshotState == "complete" {
			t.Fatal("replaced symlink granted complete capture")
		}
	}
}
