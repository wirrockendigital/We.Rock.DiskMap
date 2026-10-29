package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestMixedManifestRunsDisjointRoots exercises the actual manager and scanner, not mocked traversal.
func TestMixedManifestRunsDisjointRoots(t *testing.T) {
	m, in, source := fixture(t)
	nested := filepath.Join(source, "ownshare")
	foreign := filepath.Join(filepath.Dir(source), "foreign")
	outside := filepath.Join(filepath.Dir(source), "unselected")
	for _, p := range []string{nested, foreign, outside} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nested, "own"), []byte("four"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "other"), []byte("five!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret-marker"), []byte("unselected"), 0600); err != nil {
		t.Fatal(err)
	}
	in.Profile.Scope = contracts.Scope{Kind: "mixed", TargetIDs: []string{"vol-one", "own", "foreign"}}
	in.Roots = map[string]string{"vol-one": source, "own": nested, "foreign": foreign}
	in.VolumeIDs = map[string]string{"vol-one": "vol-one", "own": "vol-one", "foreign": "vol-two"}
	in.RootKinds = map[string]string{"vol-one": "volume", "own": "share", "foreign": "share"}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "complete" || out.Job.BytesObserved != "12" || out.ScopeManifest == nil || len(out.ScopeManifest.Roots) != 2 {
		t.Fatalf("bad mixed outcome %+v", out)
	}
	db, err := index.Open(filepath.Join(m.state, "generations", *out.Job.ResultSnapshotID, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	files := 0
	for _, record := range fixtureNodes(t, db) {
		n := record.Node
		if n.OriginalName == "secret-marker" {
			t.Fatal("foreign source enumerated")
		}
		if n.Kind == "file" {
			files++
		}
		if n.VolumeID == "vol-one" && n.ShareID != nil {
			t.Fatal("volume node incorrectly typed as share")
		}
		if n.VolumeID == "vol-two" && (n.ShareID == nil || *n.ShareID != "foreign") {
			t.Fatal("foreign share identity lost")
		}
	}
	if files != 3 {
		t.Fatalf("double enumeration %d", files)
	}
	// The manifest is stored before execution and survives manager restart unchanged.
	db.Close()
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), m.state)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.Get(out.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(out.ScopeManifest)
	b, _ := json.Marshal(stored.ScopeManifest)
	if string(a) != string(b) {
		t.Fatal("manifest changed after restart")
	}
}

// TestCoveredUnavailablePreventsPublication retains a prior complete generation when a covered share disappears.
func TestCoveredUnavailablePreventsPublication(t *testing.T) {
	m, in, source := fixture(t)
	in.Profile.Scope = contracts.Scope{Kind: "mixed", TargetIDs: []string{"vol", "share"}}
	share := filepath.Join(source, "nested")
	if err := os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	in.Roots = map[string]string{"vol": source, "share": share}
	in.RootKinds = map[string]string{"vol": "volume", "share": "share"}
	in.VolumeIDs = map[string]string{"vol": "vol", "share": "vol"}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	first, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	baseline := terminal(t, m, first.Job.ID)
	var raw string
	if err = m.db.QueryRow(`SELECT sources_json FROM profile_sources WHERE profile_id=?`, in.Profile.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	sources, err := loadSources(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range sources.Qualified {
		if sources.Qualified[i].TargetID == "share" {
			sources.Qualified[i].State = "locked"
		}
	}
	encoded, _ := json.Marshal(sources)
	if _, err = m.db.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(encoded), in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "partial" || out.CoverageErrors != 1 || len(out.ScopeManifest.Issues) != 1 {
		t.Fatalf("lost coverage %+v", out)
	}
	var active string
	if err = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active); err != nil || active != *baseline.Job.ResultSnapshotID {
		t.Fatal("partial replaced complete")
	}
}

// TestMandatoryStateBoundary proves the actual index cannot enumerate its own changing state.
func TestMandatoryStateBoundary(t *testing.T) {
	m, in, source := fixture(t)
	volume := filepath.Dir(source)
	in.Profile.Scope = contracts.Scope{Kind: "volumes", TargetIDs: []string{"volume"}}
	in.Roots = map[string]string{"volume": volume}
	in.VolumeIDs = map[string]string{"volume": "volume"}
	in.RootKinds = map[string]string{"volume": "volume"}
	if err := os.WriteFile(filepath.Join(m.state, "private-marker"), []byte("must not index"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	out := terminal(t, m, run.Job.ID)
	if out.SnapshotState != "complete" || out.Job.BytesObserved != "3" || len(out.ScopeManifest.Exclusions) != 1 || out.ScopeManifest.Exclusions[0].RelativePath != "state" {
		t.Fatalf("bad state exclusion %+v", out)
	}
	db, err := index.Open(filepath.Join(m.state, "generations", *out.Job.ResultSnapshotID, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, record := range fixtureNodes(t, db) {
		if string(record.Path) == "state" || strings.HasPrefix(string(record.Path), "state/") {
			t.Fatal("package state entered index")
		}
	}
}

// TestExplicitImportBoundary requires a deliberate new import mode and never adopts legacy profile bindings.
func TestExplicitImportBoundary(t *testing.T) {
	m, in, _ := fixture(t)
	in.SourceMode = ""
	if err := m.ImportProfile(in); err == nil {
		t.Fatal("implicit mode accepted")
	}
	in.SourceMode = "native_inventory"
	if err := m.ImportProfile(in); err == nil {
		t.Fatal("native caller paths accepted")
	}
	in.SourceMode = "isolated_roots"
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`DELETE FROM profile_sources WHERE profile_id=?`, in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(in.Profile.ID, "full"); err == nil || err.Error() != "PROFILE_REIMPORT_REQUIRED" {
		t.Fatal("old profile silently adopted", err)
	}
}
