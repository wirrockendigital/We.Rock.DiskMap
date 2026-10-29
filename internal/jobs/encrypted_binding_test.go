package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// encryptedFixture uses real descriptor bindings and explicitly injected native mount evidence.
func encryptedFixture(t *testing.T) (*Manager, Import, *profileSources, profileSources, map[string]scanner.RootBinding) {
	t.Helper()
	m, in, c := bootFixture(t)
	_, bindings := storedBinding(t, m, in.Profile.ID)
	id := in.Profile.Scope.TargetIDs[0]
	b := bindings[id]
	yes := true
	c.Shares[0].Encrypted = &yes
	fsPath := "/" + filepath.Base(b.Path)
	c.Shares[0].FileStationPath = &fsPath
	v := scanner.RootBinding{Path: c.Volumes[0].MountPath, Device: "34", Inode: "256", MountID: "40"}
	backing := scanner.RootBinding{Path: filepath.Join(v.Path, "@"+filepath.Base(b.Path)+"@"), Device: "48", Inode: "256", MountID: "40"}
	proof := inventory.EncryptedMount{Version: 1, VolumeID: c.Volumes[0].ID, Volume: v, Backing: backing, Exposed: b, Mount: inventory.Mount{ID: b.MountID, ParentID: "40", Root: "/", Path: b.Path, Filesystem: "ecryptfs", Source: backing.Path}}
	c.EncryptedMounts = map[string]inventory.EncryptedMount{id: proof}
	before := detachedCatalog(*c)
	stampNativeBindings(&before, in.Profile, bindings)
	if len(before.BindingMounts) != 1 {
		t.Fatal("fixture proof not qualified")
	}
	return m, in, c, before, bindings
}

// TestEncryptedRemountGate exercises positive changed-ID capture and separate denial causes.
func TestEncryptedRemountGate(t *testing.T) {
	m, in, c, before, bindings := encryptedFixture(t)
	defer m.Close()
	id := in.Profile.Scope.TargetIDs[0]
	old := bindings[id]
	old.MountID = "old-mount"
	bindings[id] = old
	for _, tc := range []struct {
		name   string
		change func(*profileSources, *profileSources)
		allow  bool
	}{
		{"qualified", func(*profileSources, *profileSources) {}, true},
		{"no-baseline", func(a, b *profileSources) { a.BindingMounts = nil }, false},
		{"unknown-boot", func(a, b *profileSources) { a.BindingBoots[id] = "" }, false},
		{"backing-replaced", func(a, b *profileSources) {
			p := b.EncryptedMounts[id]
			p.Backing.Inode = "other"
			b.EncryptedMounts[id] = p
		}, false},
		{"volume-replaced", func(a, b *profileSources) {
			p := b.EncryptedMounts[id]
			p.Volume.Device = "other"
			b.EncryptedMounts[id] = p
		}, false},
		{"foreign-source", func(a, b *profileSources) {
			p := b.EncryptedMounts[id]
			p.Mount.Source += "other"
			b.EncryptedMounts[id] = p
		}, false},
		{"bind-root", func(a, b *profileSources) {
			p := b.EncryptedMounts[id]
			p.Mount.Root = "/nested"
			b.EncryptedMounts[id] = p
		}, false},
		{"unknown-proof", func(a, b *profileSources) { b.EncryptedMounts = nil }, false},
		{"plain", func(a, b *profileSources) { v := false; b.Shares[0].Encrypted = &v }, false},
		{"mapping", func(a, b *profileSources) { v := "/other"; b.Shares[0].FileStationPath = &v }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, next := detachedCatalog(before), detachedCatalog(*c)
			tc.change(&a, &next)
			read := func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(next), nil }
			got, err := captureScope(context.Background(), in.Profile, a, bindings, m.state, read)
			if err != nil {
				t.Fatal(err)
			}
			if (len(got.Manifest.Issues) == 0) != tc.allow {
				t.Fatalf("issues=%+v", got.Manifest.Issues)
			}
			if !tc.allow && got.Bindings[id] != old {
				t.Fatal("failed capture advanced binding")
			}
		})
	}
}

// TestEncryptedLockCapturePreservesAuthority proves locked observations cannot erase the accepted backing baseline.
func TestEncryptedLockCapturePreservesAuthority(t *testing.T) {
	m, in, c, before, bindings := encryptedFixture(t)
	defer m.Close()
	id := in.Profile.Scope.TargetIDs[0]
	locked := detachedCatalog(*c)
	yes := true
	locked.Shares[0].State = "locked"
	locked.Shares[0].Readable = false
	locked.Shares[0].Locked = &yes
	locked.Shares[0].FileStationPath = nil
	locked.EncryptedMounts = nil
	got, err := captureScope(context.Background(), in.Profile, before, bindings, m.state, func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(locked), nil })
	if err != nil || len(got.Manifest.Issues) == 0 {
		t.Fatalf("locked %+v %v", got.Manifest, err)
	}
	if got.Sources.BindingMounts[id] != before.BindingMounts[id] {
		t.Fatal("locked authority replaced")
	}
	old := bindings[id]
	old.MountID = "old"
	bindings[id] = old
	recovered, err := captureScope(context.Background(), in.Profile, got.Sources, bindings, m.state, m.catalog)
	if err != nil || len(recovered.Manifest.Issues) != 0 {
		t.Fatalf("unlock %+v %v", recovered.Manifest, err)
	}
	// Round-tripping the persisted source DTO retains independent backing proof.
	raw, _ := json.Marshal(recovered.Sources)
	decoded, e := loadSources(string(raw))
	if e != nil || decoded.BindingMounts[id] != before.BindingMounts[id] {
		t.Fatal("proof persistence lost")
	}
}

// TestEncryptedCaptureRaceAndReusedID protects both fresh-capture confirmation and the final scan boundary.
func TestEncryptedCaptureRaceAndReusedID(t *testing.T) {
	m, in, c, before, bindings := encryptedFixture(t)
	defer m.Close()
	id := in.Profile.Scope.TargetIDs[0]
	changed := detachedCatalog(*c)
	p := changed.EncryptedMounts[id]
	p.Backing.Inode = "changed"
	changed.EncryptedMounts[id] = p
	calls := 0
	_, err := captureScope(context.Background(), in.Profile, before, bindings, m.state, func(context.Context, profileSources) (profileSources, error) {
		calls++
		if calls > 1 {
			return changed, nil
		}
		return detachedCatalog(*c), nil
	})
	if !errors.Is(err, ErrRootRebind) {
		t.Fatalf("capture race accepted %v", err)
	}
	good, err := captureScope(context.Background(), in.Profile, before, bindings, m.state, m.catalog)
	if err != nil {
		t.Fatal(err)
	}
	gaps, _ := validateScopeEnd(context.Background(), in.Profile, good.Manifest, good.Sources, func(context.Context, profileSources) (profileSources, error) { return changed, nil })
	if len(gaps) == 0 {
		t.Fatal("end backing change with reused exposed ID accepted")
	}
	denied, err := captureScope(context.Background(), in.Profile, before, bindings, m.state, func(context.Context, profileSources) (profileSources, error) { return changed, nil })
	if err != nil || len(denied.Manifest.Issues) == 0 {
		t.Fatal("same exposed ID hid backing replacement")
	}
	if denied.Sources.BindingMounts[id] != before.BindingMounts[id] {
		t.Fatal("rejected proof poisoned baseline")
	}
}

// TestEncryptedRemountPublicationAndRestart preserves last-good state and persistent authority across partial runs.
func TestEncryptedRemountPublicationAndRestart(t *testing.T) {
	m, in, c, before, bindings := encryptedFixture(t)
	id := in.Profile.Scope.TargetIDs[0]
	raw, _ := json.Marshal(before)
	if _, e := m.db.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(raw), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	start, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	baseline := terminal(t, m, start.Job.ID)
	if baseline.SnapshotState != "complete" {
		t.Fatalf("baseline %+v", baseline)
	}
	saved := detachedCatalog(*c)
	yes := true
	c.Shares[0].Locked = &yes
	c.Shares[0].State = "locked"
	c.Shares[0].Readable = false
	c.Shares[0].FileStationPath = nil
	c.EncryptedMounts = nil
	start, e = m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	partial := terminal(t, m, start.Job.ID)
	if partial.SnapshotState != "partial" {
		t.Fatalf("locked %+v", partial)
	}
	var active string
	if e = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active); e != nil || active != *baseline.Job.ResultSnapshotID {
		t.Fatal("last good changed", e)
	}
	*c = saved
	// Simulate an earlier mount identity in the durable baseline; the current descriptor remains real.
	b := bindings[id]
	b.MountID = "prior-mount"
	bindings[id] = b
	raw, _ = json.Marshal(bindings)
	if _, e = m.db.Exec(`UPDATE profiles SET roots_json=? WHERE id=?`, string(raw), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	state := m.state
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	reopened.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*c), nil }
	start, e = reopened.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	result := terminal(t, reopened, start.Job.ID)
	if result.SnapshotState != "complete" || result.Job.BytesObserved != "3" {
		t.Fatalf("recovered %+v", result)
	}
	stored, fresh := storedBinding(t, reopened, in.Profile.ID)
	if stored.BindingMounts[id] != before.BindingMounts[id] || fresh[id].MountID == "prior-mount" {
		t.Fatal("authority not persisted")
	}
}

// TestEncryptedRemountAtomicAdmission rolls back authority with a failed replay insertion.
func TestEncryptedRemountAtomicAdmission(t *testing.T) {
	m, in, _, before, bindings := encryptedFixture(t)
	defer m.Close()
	id := in.Profile.Scope.TargetIDs[0]
	b := bindings[id]
	b.MountID = "prior-mount"
	bindings[id] = b
	source, _ := json.Marshal(before)
	roots, _ := json.Marshal(bindings)
	if _, e := m.db.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(source), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`UPDATE profiles SET roots_json=? WHERE id=?`, string(roots), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`CREATE TRIGGER reject_remount_replay BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'injected'); END`); e != nil {
		t.Fatal(e)
	}
	intent := BrowserStartIdentity{Principal: "admin", Key: "encrypted-remount"}
	if _, e := m.StartBrowser(in.Profile.ID, "full", intent); e == nil {
		t.Fatal("failed transaction accepted")
	}
	after, got := storedBinding(t, m, in.Profile.ID)
	raw, _ := json.Marshal(after)
	if string(raw) != string(source) || got[id] != b {
		t.Fatal("failed admission advanced authority")
	}
	if _, e := m.db.Exec(`DROP TRIGGER reject_remount_replay`); e != nil {
		t.Fatal(e)
	}
	first, e := m.StartBrowser(in.Profile.ID, "full", intent)
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, first.Job.ID)
	replay, e := m.StartBrowser(in.Profile.ID, "full", intent)
	if e != nil || replay.Job.ID != first.Job.ID || !replay.Deduplicated {
		t.Fatal("replay recaptured", e)
	}
}
