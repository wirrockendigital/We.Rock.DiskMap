package scanner

import (
	"context"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestSpecialMetadataAndOwnBoundary uses real links, FIFO and socket without reading their contents.
func TestSpecialMetadataAndOwnBoundary(t *testing.T) {
	tmp, err := os.MkdirTemp("/tmp", "diskmap-special-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	source, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".hidden", "@ordinary", "@eaDir", "#recycle", "own-state"} {
		if err := os.Mkdir(filepath.Join(source, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, name, "data"), []byte("123"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(source, "hard-a"), []byte("12"), 0600)
	if err := os.Link(filepath.Join(source, "hard-a"), filepath.Join(source, "hard-b")); err != nil {
		t.Fatal(err)
	}
	os.Symlink(".", filepath.Join(source, "loop"))
	if err := syscall.Mkfifo(filepath.Join(source, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(source, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	r, b, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	manifest := scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume", PhysicalPath: source}, Binding: &scopemanifest.RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}}}, Exclusions: []scopemanifest.FixedExclusion{{TargetID: "volume", RelativePath: "own-state", Code: "PACKAGE_STATE_EXCLUDED"}}}
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := ScanManifest(context.Background(), db, "special", contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}, manifest, index.DefaultFileTypeMapping())
	if err != nil || result.Errors != 0 || result.Bytes != "16" || result.Entries != 14 {
		t.Fatal(result, err)
	}
	rows, err := db.Query(`SELECT id FROM nodes ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	seen := map[string]contracts.Node{}
	for _, id := range ids {
		record, err := index.ReadNode(context.Background(), db, strconv.FormatInt(id, 10))
		if err != nil {
			t.Fatal(err)
		}
		seen[record.Node.RelativePath] = record.Node
	}
	for name, kind := range map[string]string{"pipe": "fifo", "socket": "socket", "loop": "symlink"} {
		n := seen[name]
		if n.Kind != kind || n.LogicalBytes != nil || n.AllocatedBytes != nil {
			t.Fatal(name, n)
		}
	}
	for _, name := range []string{"hard-a", "hard-b"} {
		n := seen[name]
		if n.LinkCount == nil || *n.LinkCount != 2 || n.LinkGroupID != nil || n.LogicalBytes == nil || *n.LogicalBytes != "2" {
			t.Fatal(n)
		}
	}
	if _, ok := seen["own-state"]; ok {
		t.Fatal("own index entered")
	}
	var code string
	if err := db.QueryRow(`SELECT reason_code FROM scan_boundaries`).Scan(&code); err != nil || code != "PACKAGE_STATE_EXCLUDED" {
		t.Fatal(code, err)
	}
}

// TestSpecialTypeBits verifies device classification without opening or creating a device.
func TestSpecialTypeBits(t *testing.T) {
	for mode, want := range map[fs.FileMode]string{fs.ModeDevice: "blockDevice", fs.ModeDevice | fs.ModeCharDevice: "characterDevice", fs.ModeNamedPipe: "fifo", fs.ModeSocket: "socket", fs.ModeSymlink: "symlink", fs.ModeDir: "directory", 0: "file"} {
		if got := metadataKind(mode); got != want {
			t.Fatal(mode, got, want)
		}
	}
}
