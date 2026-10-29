package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestSpecialFilesAndGenerationIdentity verifies canonical node types and generation-bound ancestry.
func TestSpecialFilesAndGenerationIdentity(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "file"), []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(filepath.Join(source, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	descriptor, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	descriptor.Close()
	profile := contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}}
	var previous map[string]contracts.Node
	for _, snapshot := range []string{"first", "second"} {
		db, err := index.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		result, err := Scan(context.Background(), db, snapshot, profile, map[string]RootBinding{"share": binding}, map[string]string{"share": "volume"})
		if err != nil || result.Errors != 0 || result.Entries != 3 || result.Bytes != "4" {
			db.Close()
			t.Fatal(result, err)
		}
		var version int
		if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != index.GenerationSchema {
			t.Fatal(version, err)
		}
		nodes := map[string]contracts.Node{}
		for _, relative := range []string{".", "file", "pipe"} {
			record, e := index.FindNode(context.Background(), db, "share", []byte(relative))
			if e != nil {
				t.Fatal(e)
			}
			node := record.Node
			if contracts.ValidateNodeType(node) != nil {
				t.Fatal("invalid node kind", node.Kind)
			}
			id, e := strconv.ParseInt(node.ID, 10, 64)
			if e != nil || id <= 0 || id != record.RowID {
				t.Fatal("invalid numeric identity", node.ID, e)
			}
			info, e := os.Lstat(filepath.Join(source, relative))
			if e != nil || !SameObject(info, record.ObjectIdentity) {
				t.Fatal("unbound object identity", relative, e)
			}
			nodes[node.RelativePath] = node
		}
		child := nodes["file"]
		root := nodes["."]
		if child.ParentID == nil || *child.ParentID != root.ID || root.ParentID != nil {
			t.Fatal("invalid generation ancestry", nodes)
		}
		for path, node := range nodes {
			if node.SnapshotID != snapshot {
				t.Fatal(node)
			}
			// Numeric IDs are local to their generation; the snapshot remains part of every public binding.
			if previous != nil && node.SnapshotID == previous[path].SnapshotID {
				t.Fatal("snapshot binding reused", path)
			}
		}
		var state string
		var gaps int
		if err = db.QueryRow(`SELECT state,errors FROM coverage WHERE target_id='share'`).Scan(&state, &gaps); err != nil || state != "complete" || gaps != 0 {
			t.Fatal(state, gaps, err)
		}
		db.Close()
		previous = nodes
	}
}

// TestDescriptorPinsRootAndRejectsAncestorLinks checks both rename resistance and every path component.
func TestDescriptorPinsRootAndRejectsAncestorLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(base, "parent")
	source := filepath.Join(parent, "root")
	if err = os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "original"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = os.Rename(parent, parent+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "replacement"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = root.Stat("original"); err != nil {
		t.Fatal("descriptor lost original root", err)
	}
	if _, err = root.Stat("replacement"); !os.IsNotExist(err) {
		t.Fatal("descriptor followed replaced path", err)
	}
	if changed, err := openVerifiedRoot(binding); err == nil {
		changed.Close()
		t.Fatal("replacement identity accepted")
	}
	if err = os.Rename(parent, parent+"-replacement"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(parent+"-old", parent); err != nil {
		t.Fatal(err)
	}
	if linked, _, err := OpenBoundRoot(source); err == nil {
		linked.Close()
		t.Fatal("symlink ancestor accepted")
	}
}
