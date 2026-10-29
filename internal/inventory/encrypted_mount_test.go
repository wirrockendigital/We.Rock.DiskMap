package inventory

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"testing"
)

// TestEncryptedMountQualification rejects mismatched kernel relationships independently of job policy.
func TestEncryptedMountQualification(t *testing.T) {
	yes, no := true, false
	path := "/own"
	fs := "btrfs"
	v := contracts.Volume{ID: "volume-11111111-1111-1111-1111-111111111111", MountPath: "/volume1", Filesystem: &fs, State: "reachable"}
	s := contracts.Share{ID: "share-22222222-2222-2222-2222-222222222222", IdentityEvidence: "DSM90080:SYNO@.attr:22222222-2222-2222-2222-222222222222", VolumeID: v.ID, PhysicalPath: "/volume1/own", FileStationPath: &path, Encrypted: &yes, Locked: &no, Readable: true, State: "reachable"}
	p := EncryptedMount{Version: 1, VolumeID: v.ID, Volume: scanner.RootBinding{Path: "/volume1", Device: "34", Inode: "256", MountID: "40"}, Backing: scanner.RootBinding{Path: "/volume1/@own@", Device: "48", Inode: "256", MountID: "40"}, Exposed: scanner.RootBinding{Path: s.PhysicalPath, Device: "52", Inode: "256", MountID: "45"}, Mount: Mount{ID: "45", ParentID: "40", Path: s.PhysicalPath, Root: "/", Filesystem: "ecryptfs", Source: "/volume1/@own@"}}
	if !ValidEncryptedMount(p, v, s) {
		t.Fatal("qualified fixture rejected")
	}
	for _, change := range []func(*EncryptedMount){func(p *EncryptedMount) { p.Mount.Source = "/volume1/@other@" }, func(p *EncryptedMount) { p.Mount.ParentID = "41" }, func(p *EncryptedMount) { p.Backing.MountID = "41" }, func(p *EncryptedMount) { p.Mount.ID = "46" }, func(p *EncryptedMount) { p.Mount.Root = "/nested" }, func(p *EncryptedMount) { p.Exposed.Path = "/volume1/other" }, func(p *EncryptedMount) { p.Mount.Filesystem = "btrfs" }, func(p *EncryptedMount) { p.Backing.Inode = "" }} {
		changed := p
		change(&changed)
		if ValidEncryptedMount(changed, v, s) {
			t.Fatalf("accepted %+v", changed)
		}
	}
	for _, id := range []string{"", "share-", "x", "share-invalid"} {
		bad := s
		bad.ID = id
		if ValidEncryptedMount(p, v, bad) {
			t.Fatal("invalid UUID accepted")
		}
	}
}
