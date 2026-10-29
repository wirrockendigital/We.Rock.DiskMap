package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestNativeSourceProvenanceSealed exercises production capture, traversal and immutable header reads.
func TestNativeSourceProvenanceSealed(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	catalog.Volumes[0].Name = "Historical volume"
	for _, name := range []string{"first", "second"} {
		dir := filepath.Join(filepath.Dir(source), name)
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "payload"), []byte("12345"), 0600); e != nil {
			t.Fatal(e)
		}
		catalog.Shares = append(catalog.Shares, contracts.Share{ID: name, Name: "Historical " + name, VolumeID: "volume-a", PhysicalPath: dir, State: "reachable", Readable: true})
	}
	catalog.Shares = append(catalog.Shares, contracts.Share{ID: "missing", Name: "Missing area", VolumeID: "volume-a", PhysicalPath: filepath.Join(filepath.Dir(source), "missing"), State: "missing"})
	submitted, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, submitted.Job.ID)
	if done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	db, release, e := m.generationDB(*done.Job.ResultSnapshotID)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	h, e := index.ReadGenerationHeader(context.Background(), db, *done.Job.ResultSnapshotID)
	if e != nil {
		t.Fatal(e)
	}
	p := h.Manifest.Provenance
	if h.SchemaVersion != index.GenerationSchema || p.Authority != "native-inventory" || len(p.Roots) != 4 {
		t.Fatal(p)
	}
	for _, root := range p.Roots {
		if root.Kind != "share" || root.VolumeName != "Historical volume" || len(root.Areas) != 0 {
			t.Fatal(root)
		}
		if root.TargetID == "missing" {
			if root.Binding != nil {
				t.Fatal(root)
			}
		} else if root.Binding == nil {
			t.Fatal(root)
		}
	}
	before, _ := json.Marshal(p)
	catalog.Shares[1].Name = "Renamed"
	catalog.Shares[1].PhysicalPath = filepath.Join(filepath.Dir(source), "moved")
	if e = os.Rename(filepath.Join(filepath.Dir(source), "first"), catalog.Shares[1].PhysicalPath); e != nil {
		t.Fatal(e)
	}
	catalog.Shares = catalog.Shares[:1]
	again, e := index.ReadGenerationHeader(context.Background(), db, *done.Job.ResultSnapshotID)
	after, _ := json.Marshal(again.Manifest.Provenance)
	if e != nil || string(before) != string(after) {
		t.Fatal("historical authority changed", e)
	}
}

// TestNativeSourceProvenanceRejectsSymlinkQualification does not turn a registry label into traversal authority.
func TestNativeSourceProvenanceRejectsSymlinkQualification(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	outside := t.TempDir()
	link := filepath.Join(source, "alias")
	if e := os.Symlink(outside, link); e != nil {
		t.Fatal(e)
	}
	catalog.Shares = []contracts.Share{{ID: "alias", Name: "Alias", VolumeID: "volume-a", PhysicalPath: link, State: "reachable", Readable: true}}
	o, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, o.Job.ID)
	if done.ScopeManifest == nil {
		t.Fatal(done)
	}
	for _, root := range done.ScopeManifest.Provenance.Roots {
		if root.TargetID == "alias" && root.Binding != nil {
			t.Fatal("symlink qualified", root)
		}
	}
	if done.SnapshotState != "partial" {
		t.Fatal("unreadable root presented as complete")
	}

}

// TestNativeSourceProvenanceEndReplacement detects changed areas after traversal without mutating capture evidence.
func TestNativeSourceProvenanceEndReplacement(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	share := filepath.Join(source, "share")
	if err := os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "payload"), []byte("measured"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Shares[0].Name = "Share"
	submitted, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, submitted.Job.ID)
	if done.ScopeManifest == nil || done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	manifest := *done.ScopeManifest
	root := manifest.Provenance.Roots[0]
	if root.Kind != "share" || root.Binding == nil {
		t.Fatal(root)
	}
	before, _ := json.Marshal(manifest)
	started := detachedCatalog(*catalog)
	gaps, cancelled := validateScopeEnd(context.Background(), in.Profile, manifest, started, m.catalog)
	if cancelled || len(gaps) != 0 {
		t.Fatal(gaps, cancelled)
	}
	// Keep registry fields and volume identity unchanged while replacing only the traversed directory.
	if err := os.Rename(source, source+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	gaps, cancelled = validateScopeEnd(context.Background(), in.Profile, manifest, started, m.catalog)
	found := false
	for _, gap := range gaps {
		if gap.TargetID == "share-initial" && gap.Code == "SCOPE_CHANGED" {
			found = true
		}
	}
	after, _ := json.Marshal(manifest)
	if cancelled || !found || string(before) != string(after) {
		t.Fatal(gaps, cancelled, "capture must remain immutable")
	}
}
