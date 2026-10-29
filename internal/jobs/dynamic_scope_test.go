package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// detachedCatalog imitates independent native observations while sources remain real temporary directories.
func detachedCatalog(value profileSources) profileSources {
	raw, _ := json.Marshal(value)
	var copy profileSources
	_ = json.Unmarshal(raw, &copy)
	return copy
}

// nativeFixture imports only through the production native-selection path with an injected catalog reader.
func nativeFixture(t *testing.T, kind string) (*Manager, Import, string, *profileSources) {
	m, in, source := fixture(t)
	catalog := &profileSources{Version: 1, Mode: "native_inventory", Volumes: []contracts.Volume{{ID: "volume-a", MountPath: filepath.Dir(source), State: "reachable"}}, Shares: []contracts.Share{{ID: "share-initial", VolumeID: "volume-a", PhysicalPath: source, State: "reachable", Readable: true}}}
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	in.SourceMode = "native_inventory"
	in.Roots = nil
	in.RootKinds = nil
	in.VolumeIDs = nil
	in.Profile.Scope = contracts.Scope{Kind: kind}
	if kind != "nas" {
		in.Profile.Scope.TargetIDs = []string{"volume-a"}
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	return m, in, source, catalog
}

// TestDynamicNASRemembersNewVolumes proves first binding, next-run inclusion, and missing-target coverage.
func TestDynamicNASRemembersNewVolumes(t *testing.T) {
	m, in, _, catalog := nativeFixture(t, "nas")
	first, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	if o := terminal(t, m, first.Job.ID); o.SnapshotState != "complete" {
		t.Fatal(o)
	}
	addedBase, baseErr := filepath.EvalSymlinks(t.TempDir())
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	added := filepath.Join(addedBase, "added-share")
	if err = os.Mkdir(added, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(added, "data"), []byte("five!"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Volumes = append(catalog.Volumes, contracts.Volume{ID: "volume-b", MountPath: filepath.Dir(added), State: "reachable"})
	catalog.Shares = append(catalog.Shares, contracts.Share{ID: "share-new", VolumeID: "volume-b", PhysicalPath: added, State: "reachable", Readable: true})
	second, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	complete := terminal(t, m, second.Job.ID)
	if complete.SnapshotState != "complete" || complete.Job.BytesObserved != "8" || len(complete.ScopeManifest.Roots) != 2 || len(complete.Job.Warnings) != 1 || complete.Job.Warnings[0].Code != "SCOPE_TARGET_ADDED" {
		t.Fatalf("NAS failed to include newly authorized volume %+v", complete)
	}
	catalog.Volumes = catalog.Volumes[:1]
	catalog.Shares = catalog.Shares[:1]
	third, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	partial := terminal(t, m, third.Job.ID)
	if partial.SnapshotState != "partial" || len(partial.ScopeManifest.Roots) != 2 || partial.ScopeManifest.Roots[1].State != "missing" {
		t.Fatalf("disappeared new volume forgotten %+v", partial)
	}
	var active string
	if err = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active); err != nil || active != *complete.Job.ResultSnapshotID {
		t.Fatal("missing volume replaced complete index")
	}
}

// TestExplicitScopeDoesNotGrow proves inventory additions never expand a fixed selection.
func TestExplicitScopeDoesNotGrow(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	added := filepath.Join(filepath.Dir(source), "not-selected")
	if err := os.Mkdir(added, 0700); err != nil {
		t.Fatal(err)
	}
	catalog.Volumes = append(catalog.Volumes, contracts.Volume{ID: "volume-b", MountPath: filepath.Dir(added), State: "reachable"})
	catalog.Shares = append(catalog.Shares, contracts.Share{ID: "share-new", VolumeID: "volume-b", PhysicalPath: added, State: "reachable", Readable: true})
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "complete" || len(out.ScopeManifest.Roots) != 1 || len(out.Job.Warnings) != 0 {
		t.Fatalf("fixed selection expanded %+v", out)
	}
}

// TestKnownRootReplacementRemainsUnavailable forbids replacing a known native identity's binding silently.
func TestKnownRootReplacementRemainsUnavailable(t *testing.T) {
	m, in, source, _ := nativeFixture(t, "volumes")
	if err := os.Rename(source, source+"-previous"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "partial" || out.Job.EntriesVisited != "0" || out.ScopeManifest.Roots[0].Binding != nil {
		t.Fatalf("replacement accepted %+v", out)
	}
}

// TestNativeEndChangesRemainSeparate injects a changed second observation after real traversal.
func TestNativeEndChangesRemainSeparate(t *testing.T) {
	for _, scenario := range []string{"removed", "locked", "mapping", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			m, in, source, catalog := nativeFixture(t, "volumes")
			share := filepath.Join(source, "share")
			if err := os.Mkdir(share, 0700); err != nil {
				t.Fatal(err)
			}
			catalog.Shares = []contracts.Share{{ID: "share-initial", VolumeID: "volume-a", PhysicalPath: source, State: "reachable", Readable: true}}
			calls := 0
			m.catalog = func(context.Context, profileSources) (profileSources, error) {
				calls++
				v := detachedCatalog(*catalog)
				if calls == 2 {
					switch scenario {
					case "removed":
						v.Shares = nil
					case "locked":
						v.Shares[0].State = "locked"
						v.Shares[0].Readable = false
					case "mapping":
						v.Shares[0].PhysicalPath = filepath.Join(source, "different")
					case "unavailable":
						return profileSources{}, errors.New("private technical detail")
					}
				}
				return v, nil
			}
			run, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(run.ScopeManifest)
			out := terminal(t, m, run.Job.ID)
			after, _ := json.Marshal(out.ScopeManifest)
			if string(before) != string(after) || len(out.ScopeManifest.Issues) != 0 || out.SnapshotState != "partial" || out.CoverageErrors == 0 {
				t.Fatalf("start evidence altered or changed scope complete %+v", out)
			}
			db, err := index.Open(filepath.Join(m.state, "generations", *out.Job.ResultSnapshotID, "index.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			h, err := index.ReadGenerationHeader(context.Background(), db, *out.Job.ResultSnapshotID)
			if err != nil || len(h.EndValidation) != 1 || h.EndValidation[0].TargetID != "share-initial" || h.Coverage[0].State != "partial" {
				t.Fatalf("missing end evidence %+v %v", h, err)
			}
			var reason string
			if err := db.QueryRow(`SELECT reason_code FROM scan_boundaries WHERE target_id=? AND path_bytes=? AND reason_code=?`, h.EndValidation[0].TargetID, []byte("."), h.EndValidation[0].Code).Scan(&reason); err != nil {
				t.Fatal("missing public end gap", err)
			}
			headerStart, _ := json.Marshal(h.Manifest)
			if string(headerStart) != string(before) {
				t.Fatal("sealed header changed start capture")
			}
			if recordScopeEnd(context.Background(), db, h.EndValidation) == nil {
				t.Fatal("sealed coverage mutation accepted")
			}
		})
	}
}

// TestUnlockFirstBinding differs from replacing an already established object identity.
func TestUnlockFirstBinding(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	locked := true
	catalog.Shares = []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: source + "/share", State: "locked", Locked: &locked}}
	in.Profile.ID = "locked-share"
	in.Profile.Scope = contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source+"/share", 0700); err != nil {
		t.Fatal(err)
	}
	unlocked := false
	catalog.Shares[0].State = "reachable"
	catalog.Shares[0].Readable = true
	catalog.Shares[0].Locked = &unlocked
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "complete" || out.ScopeManifest.Roots[0].Binding == nil {
		t.Fatalf("authorized first binding blocked %+v", out)
	}
}

// TestEndRootIdentityIsRechecked protects early roots changed after their own traversal completed.
func TestEndRootIdentityIsRechecked(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	var raw, rootJSON string
	if err := m.db.QueryRow(`SELECT sources_json FROM profile_sources WHERE profile_id=?`, in.Profile.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := m.db.QueryRow(`SELECT roots_json FROM profiles WHERE id=?`, in.Profile.ID).Scan(&rootJSON); err != nil {
		t.Fatal(err)
	}
	sources, _ := loadSources(raw)
	bindings := map[string]scanner.RootBinding{}
	_ = json.Unmarshal([]byte(rootJSON), &bindings)
	capture, err := captureScope(context.Background(), in.Profile, sources, bindings, m.state, m.catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gaps, _ := validateScopeEnd(context.Background(), in.Profile, capture.Manifest, capture.Sources, m.catalog)
	if len(gaps) != 1 || gaps[0].Code != "SCOPE_CHANGED" {
		t.Fatal(gaps)
	}
}
