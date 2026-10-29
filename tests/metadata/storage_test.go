// Package metadata tests measured filesystem semantics against the real scanner.
package metadata

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// TestMeasuredStorage creates only disposable local fixtures and compares actual lstat fields.
func TestMeasuredStorage(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "source")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	normal := filepath.Join(root, "normal")
	if err = os.WriteFile(normal, bytes.Repeat([]byte{0x5a}, 8193), 0600); err != nil {
		t.Fatal(err)
	}
	sparse, err := os.OpenFile(filepath.Join(root, "sparse"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = sparse.Truncate(16 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	if err = sparse.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(normal, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("normal", filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("loop", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "zero"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	cloneErr := cloneFile(normal, filepath.Join(root, "reflink"))
	cloned := cloneErr == nil
	t.Logf("reflink_native_supported=%v probe_error=%v", cloned, cloneErr)
	descriptor, binding, err := scanner.OpenBoundRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.Close()
	database, err := index.Open(filepath.Join(base, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	profile := contracts.Profile{Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"fixture"}}}
	result, err := scanFixture(context.Background(), database, "metadata-fixture", profile, map[string]scanner.RootBinding{"fixture": binding}, map[string]string{"fixture": "observed-volume"})
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(16*1024*1024 + 2*8193)
	if cloned {
		expected += 8193
	}
	if result.Errors != 0 || result.Bytes != strconv.FormatInt(expected, 10) {
		t.Fatal("unexpected path sum", result, expected)
	}
	seen := map[string]contracts.Node{}
	for _, record := range metadataNodes(t, database) {
		node := record.Node
		seen[node.RelativePath] = node
		info, err := os.Lstat(filepath.Join(root, node.RelativePath))
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if info.Mode().IsRegular() {
			if node.LogicalBytes == nil || string(*node.LogicalBytes) != strconv.FormatInt(info.Size(), 10) {
				t.Fatal("logical size mismatch", node.RelativePath)
			}
			if node.AllocatedBytes == nil || string(*node.AllocatedBytes) != strconv.FormatInt(stat.Blocks*512, 10) {
				t.Fatal("allocated unit mismatch", node.RelativePath)
			}
			t.Logf("name=%s logical=%d st_blocks=%d allocated=%d device=%d inode=%d nlink=%d", node.RelativePath, info.Size(), stat.Blocks, stat.Blocks*512, stat.Dev, stat.Ino, stat.Nlink)
		} else if node.LogicalBytes != nil || node.AllocatedBytes != nil {
			t.Fatal("non-file received fabricated target bytes", node.RelativePath)
		}
	}
	if seen["symlink"].Kind != "symlink" || seen["loop"].Kind != "symlink" {
		t.Fatal("symlink traversal semantics changed")
	}
	normalInfo, _ := os.Lstat(normal)
	hardInfo, _ := os.Lstat(filepath.Join(root, "hardlink"))
	normalStat := normalInfo.Sys().(*syscall.Stat_t)
	hardStat := hardInfo.Sys().(*syscall.Stat_t)
	if normalStat.Dev != hardStat.Dev || normalStat.Ino != hardStat.Ino || normalStat.Nlink < 2 {
		t.Fatal("hardlink fixture not established")
	}
	if seen["normal"].ID == seen["hardlink"].ID {
		t.Fatal("path-based hardlinks collapsed")
	}
	if cloned {
		cloneInfo, err := os.Lstat(filepath.Join(root, "reflink"))
		if err != nil {
			t.Fatal(err)
		}
		if cloneInfo.Sys().(*syscall.Stat_t).Ino == normalStat.Ino {
			t.Fatal("reflink is a hardlink")
		}
	}
	after, err := os.ReadFile(normal)
	if err != nil || !bytes.Equal(after, bytes.Repeat([]byte{0x5a}, 8193)) {
		t.Fatal("scanner changed source")
	}
	t.Logf("path_logical_total=%s measured_entries=%d errors=%d", result.Bytes, result.Entries, result.Errors)
}

// TestUnknownMeasurement verifies that unsupported allocation never becomes known zero.
func TestUnknownMeasurement(t *testing.T) {
	zero := contracts.Bytes("0")
	value := struct {
		Known   *contracts.Bytes `json:"known"`
		Unknown *contracts.Bytes `json:"unknown"`
	}{&zero, nil}
	raw, err := json.Marshal(value)
	if err != nil || string(raw) != `{"known":"0","unknown":null}` {
		t.Fatal(string(raw), err)
	}
}

// TestEqualInodeSeparateMappings checks real scanner boundaries with explicitly synthetic volume labels.
func TestEqualInodeSeparateMappings(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(base, "one"), filepath.Join(base, "two")
	for _, path := range []string{first, second} {
		if err = os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(first, "data"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Link(filepath.Join(first, "data"), filepath.Join(second, "data")); err != nil {
		t.Fatal(err)
	}
	roots := map[string]scanner.RootBinding{}
	for id, path := range map[string]string{"one": first, "two": second} {
		descriptor, binding, e := scanner.OpenBoundRoot(path)
		if e != nil {
			t.Fatal(e)
		}
		descriptor.Close()
		roots[id] = binding
	}
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profile := contracts.Profile{Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"one", "two"}}}
	result, err := scanFixture(context.Background(), db, "collision-fixture", profile, roots, map[string]string{"one": "synthetic-volume-one", "two": "synthetic-volume-two"})
	if err != nil || result.Bytes != "6" {
		t.Fatal(result, err)
	}
	volumes, ids := map[string]bool{}, map[string]bool{}
	for _, record := range metadataNodes(t, db) {
		node := record.Node
		if node.ParentID == nil {
			continue
		}
		volumes[node.VolumeID] = true
		ids[node.ID] = true
	}
	if len(ids) != 2 || !volumes["synthetic-volume-one"] || !volumes["synthetic-volume-two"] {
		t.Fatal("equal inode incorrectly collapsed across mappings")
	}
}

// metadataNodes uses the production bounded numeric reader without a stored JSON or path projection.
func metadataNodes(t *testing.T, db *sql.DB) []index.NodeRecord {
	t.Helper()
	reader, err := index.NewNodeReader(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	out := []index.NodeRecord{}
	var after int64
	for {
		page, err := reader.Page(after, 128)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page...)
		if len(page) < 128 {
			return out
		}
		after = page[len(page)-1].RowID
	}
}
