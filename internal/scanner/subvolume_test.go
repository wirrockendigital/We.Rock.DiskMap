package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestSubvolumeGuardRequiresCapturedIdentity exercises the actual guard with a controlled ioctl result.
// Native ABI support is tested separately under the installed DSM package identity.
func TestSubvolumeGuardRequiresCapturedIdentity(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(base, "current-share")
	os.Mkdir(childPath, 0700)
	r, binding, err := OpenBoundRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root, err := r.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	c, cb, err := OpenBoundRoot(childPath)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	child, err := c.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	q := scopemanifest.QualifiedSubvolume{TargetID: "volume", SourceID: "share", RelativePath: "current-share", TreeID: "259", Binding: scopemanifest.RootIdentity{Path: cb.Path, Device: cb.Device, Inode: cb.Inode, MountID: cb.MountID}}
	tree := "5"
	read := func(*os.File) (string, bool, error) { return tree, true, nil }
	guard := subvolumeGuard("volume", binding, []scopemanifest.QualifiedSubvolume{q}, read)
	if err := guard(".", root); err != nil {
		t.Fatal(err)
	}
	if err := guard("@ordinary", root); err != nil {
		t.Fatal("ordinary name rejected", err)
	}
	tree = "259"
	if err := guard("current-share", child); err != nil {
		t.Fatal(err)
	}
	if err := guard("current-share/nested", child); err != nil {
		t.Fatal(err)
	}
	tree = "260"
	if guard("current-share/snapshot", child) == nil {
		t.Fatal("unknown nested tree accepted")
	}
	tree = "259"
	if guard("renamed-or-foreign", child) == nil {
		t.Fatal("tree ID accepted at unrelated path")
	}
	q.Binding.Inode = "0"
	guard = subvolumeGuard("volume", binding, []scopemanifest.QualifiedSubvolume{q}, read)
	tree = "5"
	if err := guard(".", root); err != nil {
		t.Fatal(err)
	}
	tree = "259"
	if guard("current-share", child) == nil {
		t.Fatal("replaced share binding accepted")
	}
}
