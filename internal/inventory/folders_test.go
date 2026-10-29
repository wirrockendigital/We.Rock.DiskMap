package inventory

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"os"
	"path/filepath"
	"testing"
)

// A real temporary tree exercises descriptor traversal, pagination and symlink rejection.
func TestFolderScopeAndBrowse(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(base, "share")
	if err = os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"選択", "outside"} {
		if err = os.Mkdir(filepath.Join(share, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(share, "file"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(share, "outside"), filepath.Join(share, "link")); err != nil {
		t.Fatal(err)
	}
	volumes := []contracts.Volume{{ID: "volume-a", MountPath: base, State: "reachable"}}
	shares := []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}}
	scope := contracts.Scope{Kind: "folder", TargetIDs: []string{"share-a"}, RelativePath: "選択"}
	normalized, err := Normalize(scope, volumes, shares)
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized.Roots) != 1 || normalized.Roots[0].PhysicalPath != filepath.Join(share, "選択") || normalized.Roots[0].TargetID != "share-a" {
		t.Fatalf("wrong selection: %+v", normalized)
	}
	manifest, err := FreezeScope(context.Background(), normalized, func(_ context.Context, r NormalizedRoot) (RootIdentity, error) {
		fd, b, e := OpenScopeRoot(r)
		if e != nil {
			return RootIdentity{}, e
		}
		fd.Close()
		return RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}, nil
	})
	if err != nil || manifest.Roots[0].Binding == nil {
		t.Fatalf("folder not bound: %v", err)
	}
	for _, bad := range []string{"../outside", "/outside", "選択/../outside", ".", "link", "link/child"} {
		s := scope
		s.RelativePath = bad
		n, e := Normalize(s, volumes, shares)
		if e == nil {
			fd, _, e := OpenScopeRoot(n.Roots[0])
			if e == nil {
				fd.Close()
				t.Fatalf("unsafe path accepted: %q", bad)
			}
		}
	}
	page, err := BrowseFolders(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}, volumes, shares)
	if err != nil || !ValidFolders(page) || len(page.Entries) != 2 {
		t.Fatalf("browse: %+v %v", page, err)
	}
	for i := 0; i < 130; i++ {
		if err = os.Mkdir(filepath.Join(share, "選択", fmt.Sprintf("n%03d", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	first, err := BrowseFolders(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "選択"}, volumes, shares)
	if err != nil || len(first.Entries) != 128 || first.NextCursor == nil || !ValidFolders(first) {
		t.Fatalf("first page: %v", err)
	}
	last, err := BrowseFolders(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "選択", After: first.NextCursor}, volumes, shares)
	if err != nil || len(last.Entries) != 2 || last.NextCursor != nil || !ValidFolders(last) {
		t.Fatalf("last page: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = BrowseFolders(ctx, FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}, volumes, shares); err == nil {
		t.Fatal("cancel accepted")
	}
	raw, err := os.ReadFile(filepath.Join(share, "file"))
	if err != nil || string(raw) != "unchanged" {
		t.Fatal("source changed")
	}
}
