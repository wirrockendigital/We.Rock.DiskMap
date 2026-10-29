package inventory

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"os"
	"path/filepath"
	"testing"
)

// One child folder must remain discoverable among many ordinary files.
func TestFileHeavyFolderRemainsNavigable(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	share := filepath.Join(base, "share")
	os.Mkdir(share, 0700)
	os.Mkdir(filepath.Join(share, "only-folder"), 0700)
	for i := 0; i < 4096; i++ {
		if e := os.WriteFile(filepath.Join(share, fmt.Sprintf("f%04d", i)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	out, e := BrowseFolders(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}, []contracts.Volume{{ID: "volume-a", MountPath: base, State: "reachable"}}, []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}})
	t.Logf("folders=%d error=%v", len(out.Entries), e)
	if e != nil {
		t.Errorf("cannot discover one folder among 4096 files: %v", e)
	}
}

// Cursor pages reuse a qualified listing, while changed metadata and replaced roots invalidate it.
func TestFolderBrowserCacheInvalidation(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	share := filepath.Join(base, "share")
	os.Mkdir(share, 0700)
	for i := 0; i < 260; i++ {
		if e := os.Mkdir(filepath.Join(share, fmt.Sprintf("d%03d", i)), 0700); e != nil {
			t.Fatal(e)
		}
	}
	volumes := []contracts.Volume{{ID: "volume-a", MountPath: base, State: "reachable"}}
	shares := []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}}
	browser := &FolderBrowser{}
	request := FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}
	first, e := browser.Browse(context.Background(), request, volumes, shares)
	if e != nil || len(first.Entries) != 128 {
		t.Fatal(first, e)
	}
	if len(browser.entries) != 1 {
		t.Fatal("listing not cached")
	}
	var listing folderListing
	for _, v := range browser.entries {
		listing = v
	}
	request.After = first.NextCursor
	second, e := browser.Browse(context.Background(), request, volumes, shares)
	if e != nil || len(second.Entries) != 128 {
		t.Fatal(second, e)
	}
	for _, v := range browser.entries {
		if &listing.names[0] != &v.names[0] {
			t.Fatal("cursor re-enumerated source")
		}
	}
	if e := os.Mkdir(filepath.Join(share, "new"), 0700); e != nil {
		t.Fatal(e)
	}
	request.After = second.NextCursor
	last, e := browser.Browse(context.Background(), request, volumes, shares)
	if e != nil || len(last.Entries) != 5 || last.Entries[4].Name != "new" {
		t.Fatal(last, e)
	}
	// A cancelled read cannot use cached names as an authorization shortcut.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := browser.Browse(ctx, request, volumes, shares); e == nil {
		t.Fatal("cancelled cache read succeeded")
	}
	old := share + "-old"
	if e := os.Rename(share, old); e != nil {
		t.Fatal(e)
	}
	os.Mkdir(share, 0700)
	os.Mkdir(filepath.Join(share, "replacement"), 0700)
	request.After = nil
	fresh, e := browser.Browse(context.Background(), request, volumes, shares)
	if e != nil || len(fresh.Entries) != 1 || fresh.Entries[0].Name != "replacement" {
		t.Fatal(fresh, e)
	}
}

// Compare identical complete cursor traversals with and without the service-lifetime listing cache.
func BenchmarkFolderBrowserPagination(b *testing.B) {
	base, e := filepath.EvalSymlinks(b.TempDir())
	if e != nil {
		b.Fatal(e)
	}
	share := filepath.Join(base, "share")
	os.Mkdir(share, 0700)
	for i := 0; i < 1024; i++ {
		if e := os.Mkdir(filepath.Join(share, fmt.Sprintf("d%04d", i)), 0700); e != nil {
			b.Fatal(e)
		}
	}
	volumes := []contracts.Volume{{ID: "volume-a", MountPath: base, State: "reachable"}}
	shares := []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprint(cached), func(b *testing.B) {
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				browser := &FolderBrowser{}
				request := FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}
				count := 0
				for {
					var page ScopeFolders
					var err error
					if cached {
						page, err = browser.Browse(context.Background(), request, volumes, shares)
					} else {
						page, err = BrowseFolders(context.Background(), request, volumes, shares)
					}
					if err != nil {
						b.Fatal(err)
					}
					count += len(page.Entries)
					if page.NextCursor == nil {
						break
					}
					request.After = page.NextCursor
				}
				if count != 1024 {
					b.Fatal("incomplete traversal", count)
				}
			}
		})
	}
}

// Different public share-relative paths must not reuse each other's cached relative identities.
func TestFolderBrowserAliasPaths(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Join(base, "outer", "share", "child"), 0700)
	volumes := []contracts.Volume{{ID: "volume-a", MountPath: base, State: "reachable"}}
	shares := []contracts.Share{{ID: "share-a", VolumeID: "volume-a", PhysicalPath: filepath.Join(base, "outer", "share"), State: "reachable", Readable: true}, {ID: "share-b", VolumeID: "volume-a", PhysicalPath: filepath.Join(base, "outer"), State: "reachable", Readable: true}}
	browser := &FolderBrowser{}
	first, e := browser.Browse(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: "."}, volumes, shares[:1])
	if e != nil || first.Entries[0].RelativePath != "child" {
		t.Fatal(first, e)
	}
	second, e := browser.Browse(context.Background(), FolderRequest{ContractVersion: 1, ShareID: "share-b", RelativePath: "share"}, volumes, shares[1:])
	if e != nil || !ValidFolders(second) || second.Entries[0].RelativePath != "share/child" {
		t.Fatal(second, e)
	}
}
