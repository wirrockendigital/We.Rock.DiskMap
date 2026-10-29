package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// selectionGeneration seals independent snapshots with one native share boundary.
func selectionGeneration(t *testing.T, snapshot, mount string, share bool) (*sql.DB, index.GenerationHeader) {
	t.Helper()
	ctx := context.Background()
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = initializeQueryGeneration(ctx, db, snapshot); err != nil {
		t.Fatal(err)
	}
	root := fixtureNodeID(snapshot, "volume", ".")
	dir := fixtureNodeID(snapshot, "volume", "folder")
	for _, n := range []contracts.Node{{SnapshotID: snapshot, ID: root, OriginalName: "root", Kind: "directory", VolumeID: "volume", RelativePath: "."}, {SnapshotID: snapshot, ID: dir, ParentID: &root, OriginalName: "folder", Kind: "directory", VolumeID: "volume", RelativePath: "folder"}} {
		raw, _ := json.Marshal(n)
		if _, err = insertQueryFixtureNode(db, n.ID, n.ParentID, string(raw), "volume", []byte(n.OriginalName), []byte(n.RelativePath), "1:256:0:0"); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`)
	manifest := scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume", PhysicalPath: "/fixture"}, Binding: &scopemanifest.RootIdentity{Path: "/fixture", Device: "1", Inode: "256", MountID: mount}}}, CoveredTargetIDs: []string{"volume"}}
	manifest.Provenance = scopemanifest.ExplicitProvenance(manifest, false)
	manifest.Provenance.Authority = "native-inventory"
	if share {
		id := "share-folder"
		manifest.CoveredTargetIDs = append(manifest.CoveredTargetIDs, id)
		manifest.Provenance.Roots[0].Areas = []scopemanifest.SourceArea{{SourceID: &id, VolumeID: "volume", Name: "Folder", RelativePath: []byte("folder"), State: "reachable", Qualification: "qualified", Binding: &scopemanifest.RootIdentity{Path: "/fixture/folder", Device: "1", Inode: "256", MountID: mount}}}
	}
	h := index.GenerationHeader{SnapshotID: snapshot, JobID: "job-" + snapshot, Profile: contracts.Profile{ID: "p", Revision: 1}, Manifest: manifest, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: 2, LogicalBytes: "0", FileTypeMapping: index.DefaultFileTypeMapping(), AggregateVersion: index.AggregateVersion}
	if err = index.BuildDerivedProjections(ctx, db, h.FileTypeMapping); err != nil {
		t.Fatal(err)
	}
	if err = index.FinalizeGeneration(ctx, db, h); err != nil {
		t.Fatal(err)
	}
	h, err = index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return db, h
}

// TestVirtualSelectionIdentity checks all virtual kinds across real sealed snapshot identities.
func TestVirtualSelectionIdentity(t *testing.T) {
	ctx := context.Background()
	a, ha := selectionGeneration(t, "old", "1", true)
	b, hb := selectionGeneration(t, "new", "1", true)
	changed, hc := selectionGeneration(t, "changed", "2", true)
	removed, hr := selectionGeneration(t, "removed", "1", false)
	for _, tc := range []struct{ kind, key string }{{"nas", ""}, {"volume", "volume"}, {"share", "share-folder"}, {"other", "volume"}} {
		t.Run(tc.kind, func(t *testing.T) {
			selected, err := QualifyVirtualSelection(ctx, a, ha, virtualID(ha.SnapshotID, tc.kind, tc.key))
			if err != nil || selected == nil {
				t.Fatal(selected, err)
			}
			id, err := ResolveVirtualSelection(ctx, b, hb, selected)
			if err != nil || id != virtualID(hb.SnapshotID, tc.kind, tc.key) {
				t.Fatal(id, err)
			}
			for _, target := range []struct {
				db *sql.DB
				h  index.GenerationHeader
			}{{changed, hc}, {removed, hr}} {
				id, err = ResolveVirtualSelection(ctx, target.db, target.h, selected)
				if err != nil {
					t.Fatal(err)
				}
				if (id != "") != (tc.kind == "nas") {
					t.Fatalf("changed source silently transferred: %s", id)
				}
			}
		})
	}
	for _, id := range []string{virtualID(hb.SnapshotID, "nas", ""), "q-nas-forged", fixtureNodeID(ha.SnapshotID, "volume", ".")} {
		v, err := QualifyVirtualSelection(ctx, a, ha, id)
		if err != nil || v != nil {
			t.Fatal(id, v, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := QualifyVirtualSelection(cancelled, a, ha, virtualID(ha.SnapshotID, "nas", "")); err == nil {
		t.Fatal("cancelled lookup succeeded")
	}
}
