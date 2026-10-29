package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

const bootA = "11111111-1111-1111-1111-111111111111"
const bootB = "22222222-2222-2222-2222-222222222222"

// bootFixture uses real source descriptors with qualified synthetic inventory observations.
func bootFixture(t *testing.T) (*Manager, Import, *profileSources) {
	t.Helper()
	m, in, source := fixture(t)
	fs := "btrfs"
	plain := false
	fsPath := "/source"
	vu := "33333333-3333-3333-3333-333333333333"
	su := "44444444-4444-4444-4444-444444444444"
	catalog := &profileSources{Version: 1, Mode: "native_inventory", BootID: bootA, Volumes: []contracts.Volume{{ID: "volume-" + vu, MountPath: filepath.Dir(source), State: "reachable", Filesystem: &fs, IdentityEvidence: "BTRFS_IOC_FS_INFO:" + vu + ";mount-id:1"}}, Shares: []contracts.Share{{ID: "share-" + su, VolumeID: "volume-" + vu, PhysicalPath: source, FileStationPath: &fsPath, State: "reachable", Readable: true, Encrypted: &plain, Locked: &plain, IdentityEvidence: "DSM90080:SYNO@.attr:" + su}}}
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	in.SourceMode = "native_inventory"
	in.Roots = nil
	in.VolumeIDs = nil
	in.RootKinds = nil
	in.Profile.Scope.TargetIDs = []string{"share-" + su}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	return m, in, catalog
}

// storedBinding reads the actual private baseline instead of duplicating capture logic.
func storedBinding(t *testing.T, m *Manager, id string) (profileSources, map[string]scanner.RootBinding) {
	t.Helper()
	var source, root string
	if err := m.db.QueryRow(`SELECT s.sources_json,p.roots_json FROM profile_sources s JOIN profiles p ON p.id=s.profile_id WHERE p.id=?`, id).Scan(&source, &root); err != nil {
		t.Fatal(err)
	}
	s, e := loadSources(source)
	if e != nil {
		t.Fatal(e)
	}
	var b map[string]scanner.RootBinding
	if e = json.Unmarshal([]byte(root), &b); e != nil {
		t.Fatal(e)
	}
	return s, b
}

// TestNativeBootBinding transitions ephemeral identity only after a proved different boot.
func TestNativeBootBinding(t *testing.T) {
	m, in, catalog := bootFixture(t)
	old, b := storedBinding(t, m, in.Profile.ID)
	id := in.Profile.Scope.TargetIDs[0]
	stale := b[id]
	stale.MountID += "-old"
	b[id] = stale
	for _, tc := range []struct {
		name, boot string
		allow      bool
	}{{"same", bootA, false}, {"different", bootB, true}, {"unknown", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			catalog.BootID = tc.boot
			c, e := captureScope(context.Background(), in.Profile, old, b, m.state, m.catalog)
			if e != nil {
				t.Fatal(e)
			}
			if (len(c.Manifest.Issues) == 0) != tc.allow {
				t.Fatalf("issues=%+v", c.Manifest.Issues)
			}
			if tc.allow && c.Sources.BindingBoots[id] != bootB {
				t.Fatal("epoch missing")
			}
			if !tc.allow && c.Sources.BindingBoots[id] != bootA {
				t.Fatal("failed capture advanced epoch")
			}
		})
	}
	// Failed metadata capture cannot poison the stable binding hash for a later boot.
	catalog.BootID = bootB
	changed := *catalog.Shares[0].FileStationPath + "-changed"
	catalog.Shares[0].FileStationPath = &changed
	c, e := captureScope(context.Background(), in.Profile, old, b, m.state, m.catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Manifest.Issues) == 0 {
		t.Fatal("mapping accepted")
	}
	c2, e := captureScope(context.Background(), in.Profile, c.Sources, b, m.state, m.catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(c2.Manifest.Issues) == 0 {
		t.Fatal("failed capture poisoned identity")
	}
}

// TestNativeExplicitRecovery preserves profile selection, increments revision, and rejects same-boot replacement.
func TestNativeExplicitRecovery(t *testing.T) {
	m, in, catalog := bootFixture(t)
	s, b := storedBinding(t, m, in.Profile.ID)
	s.BootID = ""
	s.BindingBoots = nil
	s.BindingIdentities = nil
	raw, _ := json.Marshal(s)
	if _, e := m.db.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, raw, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	id := in.Profile.Scope.TargetIDs[0]
	stale := b[id]
	stale.MountID += "-previous"
	b[id] = stale
	roots, _ := json.Marshal(b)
	if _, e := m.db.Exec(`UPDATE profiles SET roots_json=? WHERE id=?`, roots, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	p, e := m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1)
	if e != nil || p.Revision != 2 || p.Scope.TargetIDs[0] != id {
		t.Fatalf("%+v %v", p, e)
	}
	if _, e = m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	fresh, bindings := storedBinding(t, m, in.Profile.ID)
	if fresh.BindingBoots[id] != bootA || fresh.BindingIdentities[id] == "" {
		t.Fatal("missing provenance")
	}
	// An explicit recovery is not a same-boot path-retargeting escape hatch.
	root := catalog.Shares[0].PhysicalPath
	if e = os.Rename(root, root+"-old"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 2); !errors.Is(e, ErrRootRebind) {
		t.Fatal(e)
	}
	after, got := storedBinding(t, m, in.Profile.ID)
	if after.BindingBoots[id] != bootA || got[id] != bindings[id] {
		t.Fatal("rejected action mutated binding")
	}
}

// TestNativeBindingTOCTOU rejects changed catalog/boot after descriptor capture and end-of-run reboot.
func TestNativeBindingTOCTOU(t *testing.T) {
	m, in, catalog := bootFixture(t)
	old, b := storedBinding(t, m, in.Profile.ID)
	calls := 0
	read := func(context.Context, profileSources) (profileSources, error) {
		calls++
		v := detachedCatalog(*catalog)
		if calls >= 3 {
			v.BootID = bootB
		}
		return v, nil
	}
	if _, e := captureScope(context.Background(), in.Profile, old, b, m.state, read); !errors.Is(e, ErrRootRebind) {
		t.Fatal(e)
	}
	c, e := captureScope(context.Background(), in.Profile, old, b, m.state, m.catalog)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := json.Marshal(c.Manifest)
	catalog.BootID = bootB
	gaps, _ := validateScopeEnd(context.Background(), in.Profile, c.Manifest, c.Sources, m.catalog)
	after, _ := json.Marshal(c.Manifest)
	if len(gaps) == 0 || string(before) != string(after) {
		t.Fatal("end reboot not independently reported")
	}
	// Identity and evidence fields are required, not mere matching names or paths.
	n, e := normalizeSources(in.Profile.Scope, *catalog)
	if e != nil {
		t.Fatal(e)
	}
	r := n.Roots[0]
	for _, mutate := range []func(*profileSources){func(s *profileSources) { s.Volumes[0].IdentityEvidence = "guessed" }, func(s *profileSources) { s.Shares[0].IdentityEvidence = "guessed" }, func(s *profileSources) { s.Shares[0].VolumeID = "volume-other" }, func(s *profileSources) { s.Shares[0].PhysicalPath += "other" }} {
		v := detachedCatalog(*catalog)
		mutate(&v)
		if stableNativeRoot(old, v, r) {
			t.Fatal("unqualified identity accepted")
		}
	}
}

// TestNativeRequalificationAtomicGuards prevents queued/active captures and failed persistence from changing provenance.
func TestNativeRequalificationAtomicGuards(t *testing.T) {
	m, in, _ := bootFixture(t)
	m.mu.Lock()
	m.active = &Outcome{}
	m.mu.Unlock()
	if _, e := m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	m.mu.Lock()
	m.active = nil
	m.mu.Unlock()
	if _, e := m.db.Exec(`INSERT INTO scan_queue(job_id,profile_id,sources_json) VALUES('pending','pending','{}')`); e != nil {
		t.Fatal(e)
	}
	if _, e := m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`DELETE FROM scan_queue`); e != nil {
		t.Fatal(e)
	}
	before, b := storedBinding(t, m, in.Profile.ID)
	rawBefore, _ := json.Marshal(before)
	if _, e := m.db.Exec(`CREATE TRIGGER reject_requalification BEFORE UPDATE ON profile_sources BEGIN SELECT RAISE(ABORT,'test rejection'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1); e == nil {
		t.Fatal("expected rollback")
	}
	after, got := storedBinding(t, m, in.Profile.ID)
	rawAfter, _ := json.Marshal(after)
	if string(rawBefore) != string(rawAfter) || got[in.Profile.Scope.TargetIDs[0]] != b[in.Profile.Scope.TargetIDs[0]] {
		t.Fatal("failed persistence altered baseline")
	}
	var rev int
	if e := m.db.QueryRow(`SELECT revision FROM profiles WHERE id=?`, in.Profile.ID).Scan(&rev); e != nil || rev != 1 {
		t.Fatalf("revision %d %v", rev, e)
	}
}

// TestNativeBootUpdateAndRestart preserves the refreshed baseline and strict isolated-root behavior.
func TestNativeBootUpdateAndRestart(t *testing.T) {
	m, in, catalog := bootFixture(t)
	_, b := storedBinding(t, m, in.Profile.ID)
	id := in.Profile.Scope.TargetIDs[0]
	stale := b[id]
	stale.MountID += "-stale"
	b[id] = stale
	raw, _ := json.Marshal(b)
	if _, e := m.db.Exec(`UPDATE profiles SET roots_json=? WHERE id=?`, raw, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := m.UpdateProfile(context.Background(), in.Profile.ID, 1, in.Profile); !errors.Is(e, ErrRootRebind) {
		t.Fatal(e)
	}
	catalog.BootID = bootB
	p, e := m.UpdateProfile(context.Background(), in.Profile.ID, 1, in.Profile)
	if e != nil {
		t.Fatal(e)
	}
	baseline, bindings := storedBinding(t, m, in.Profile.ID)
	if baseline.BindingBoots[id] != bootB || bindings[id] == stale {
		t.Fatal("not rebound")
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(context.Background(), m.state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopened.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	stored, _ := storedBinding(t, reopened, in.Profile.ID)
	if stored.BindingBoots[id] != bootB {
		t.Fatal("restart lost epoch")
	}
	submitted, e := reopened.Start(p.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	out := terminal(t, reopened, submitted.Job.ID)
	if out.SnapshotState != "complete" || out.Job.BytesObserved != "3" {
		t.Fatalf("scan %+v", out)
	}
	isolated, isolatedIn, _ := fixture(t)
	if e = isolated.ImportProfile(isolatedIn); e != nil {
		t.Fatal(e)
	}
	if _, e = isolated.RequalifyNativeProfile(context.Background(), isolatedIn.Profile.ID, 1); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}

// TestNativeLegacyCaptureCannotPoisonMapping retains pre-provenance identity across a rejected capture.
func TestNativeLegacyCaptureCannotPoisonMapping(t *testing.T) {
	m, in, catalog := bootFixture(t)
	old, b := storedBinding(t, m, in.Profile.ID)
	old.BindingBoots = nil
	old.BindingIdentities = nil
	old.BootID = ""
	next := filepath.Join(filepath.Dir(catalog.Shares[0].PhysicalPath), "different")
	if e := os.Mkdir(next, 0700); e != nil {
		t.Fatal(e)
	}
	catalog.Shares[0].PhysicalPath = next
	failed, e := captureScope(context.Background(), in.Profile, old, b, m.state, m.catalog)
	if e != nil {
		t.Fatal(e)
	}
	if len(failed.Manifest.Issues) == 0 {
		t.Fatal("changed mapping accepted")
	}
	raw, _ := json.Marshal(failed.Sources)
	if _, e = m.db.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, raw, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = m.RequalifyNativeProfile(context.Background(), in.Profile.ID, 1); !errors.Is(e, ErrRootRebind) {
		t.Fatalf("legacy poisoned mapping accepted: %v", e)
	}
}
