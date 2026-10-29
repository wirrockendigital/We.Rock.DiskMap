package indexbench

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// qualificationDB uses the production generation schema, never the older synthetic benchmark schema.
func qualificationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "aggregate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = index.InitializeGeneration(context.Background(), db, "qualification"); err != nil {
		t.Fatal(err)
	}
	return db
}

// qualificationNode provides only independent raw inputs; expected aggregates are written separately.
func qualificationNode(id, parent, name, kind, logical, allocated, volume string) contracts.Node {
	var p *string
	if parent != "" {
		p = &parent
	}
	var l, a *contracts.Bytes
	if logical != "?" {
		v := contracts.Bytes(logical)
		l = &v
	}
	if allocated != "?" {
		v := contracts.Bytes(allocated)
		a = &v
	}
	return contracts.Node{SnapshotID: "qualification", ID: id, ParentID: p, OriginalName: name, RelativePath: id, Kind: kind, LogicalBytes: l, AllocatedBytes: a, VolumeID: volume}
}

// insertQualificationNodes stores path references independently, including intentional shared object identities.
func insertQualificationNodes(t *testing.T, db *sql.DB, nodes []contracts.Node) map[string]string {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	w, err := index.NewNodeWriter(context.Background(), tx, "qualification")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	roots := map[string]int64{}
	ids := map[string]string{}
	for _, node := range nodes {
		root := roots[node.VolumeID]
		if root == 0 {
			root, err = w.AddRoot(node.VolumeID, node.VolumeID, nil)
			if err != nil {
				t.Fatal(err)
			}
			roots[node.VolumeID] = root
		}
		var parent int64
		if node.ParentID != nil {
			parent, err = strconv.ParseInt(ids[*node.ParentID], 10, 64)
			if err != nil {
				t.Fatal("parent must precede child", err)
			}
		}
		id, e := w.Insert(root, parent, []byte(node.OriginalName), node.Kind, node.LogicalBytes, node.AllocatedBytes, "shared-inode", node.LinkCount)
		if e != nil {
			t.Fatal(e)
		}
		ids[node.ID] = strconv.FormatInt(id, 10)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// metric checks exact decimal strings, preserving both known bytes and independently unknown file counts.
func metric(t *testing.T, got contracts.MetricAggregate, known, unknown string) {
	t.Helper()
	if string(got.KnownBytes) != known || string(got.UnknownFileCount) != unknown {
		t.Fatalf("metric got %+v want known=%s unknown=%s", got, known, unknown)
	}
}

// content checks hand-calculated counts; directories themselves never supply regular-file payload bytes.
func content(t *testing.T, got contracts.ContentAggregate, files, dirs, specials, logical, logicalUnknown, allocated, allocatedUnknown string) {
	t.Helper()
	if string(got.FileCount) != files || string(got.DirectoryCount) != dirs || string(got.SpecialCount) != specials {
		t.Fatalf("counts got %+v want files=%s directories=%s specials=%s", got, files, dirs, specials)
	}
	metric(t, got.Logical, logical, logicalUnknown)
	metric(t, got.Allocated, allocated, allocatedUnknown)
}

// TestAggregateHandCalculatedTree uses explicit arithmetic rather than duplicating the product traversal.
func TestAggregateHandCalculatedTree(t *testing.T) {
	db := qualificationDB(t)
	nodes := []contracts.Node{
		qualificationNode("root-a", "", "root-a", "directory", "?", "?", "volume-a"),
		qualificationNode("direct", "root-a", "direct.txt", "file", "10", "16", "volume-a"),
		qualificationNode("hard-a", "root-a", "hard-a.bin", "file", "7", "8", "volume-a"),
		qualificationNode("hard-b", "root-a", "hard-b.bin", "file", "7", "8", "volume-a"),
		qualificationNode("unknown", "root-a", "unknown", "file", "?", "?", "volume-a"),
		qualificationNode("pipe", "root-a", "pipe", "fifo", "?", "?", "volume-a"),
		qualificationNode("link", "root-a", "link", "symlink", "?", "?", "volume-a"),
		qualificationNode("empty", "root-a", "empty", "directory", "?", "?", "volume-a"),
		qualificationNode("nested", "root-a", "nested", "directory", "?", "?", "volume-a"),
		qualificationNode("huge", "nested", "huge.bin", "file", "9007199254740993", "9007199254740992", "volume-a"),
		qualificationNode("root-b", "", "root-b", "directory", "?", "?", "volume-b"),
		qualificationNode("same-inode", "root-b", "same-inode.bin", "file", "7", "8", "volume-b"),
		qualificationNode("logical-only", "root-b", "logical.txt", "file", "11", "?", "volume-b"),
		qualificationNode("allocated-only", "root-b", "allocated.txt", "file", "?", "16", "volume-b"),
	}
	links := 2
	nodes[2].LinkCount = &links
	nodes[3].LinkCount = &links
	ids := insertQualificationNodes(t, db, nodes)
	mapping := contracts.FileTypeMapping{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "txt", Category: "document"}}}
	if err := index.BuildDerivedProjections(context.Background(), db, mapping); err != nil {
		t.Fatal(err)
	}
	root, err := index.ReadDirectoryAggregate(context.Background(), db, ids["root-a"])
	if err != nil {
		t.Fatal(err)
	}
	content(t, root.Direct, "4", "2", "2", "24", "1", "32", "1")
	content(t, root.Subtree, "5", "2", "2", "9007199254741017", "1", "9007199254741024", "1")
	document := root.Subtree.Types["document"]
	if string(document.FileCount) != "1" {
		t.Fatal(document)
	}
	metric(t, document.Logical, "10", "0")
	metric(t, document.Allocated, "16", "0")
	other := root.Subtree.Types["otherUnknown"]
	if string(other.FileCount) != "4" {
		t.Fatal(other)
	}
	metric(t, other.Logical, "9007199254741007", "1")
	metric(t, other.Allocated, "9007199254741008", "1")
	if _, exists := root.Subtree.Types["image"]; exists {
		t.Fatal("empty type category must remain sparse")
	}
	empty, err := index.ReadDirectoryAggregate(context.Background(), db, ids["empty"])
	if err != nil {
		t.Fatal(err)
	}
	content(t, empty.Direct, "0", "0", "0", "0", "0", "0", "0")
	content(t, empty.Subtree, "0", "0", "0", "0", "0", "0", "0")
	nested, err := index.ReadDirectoryAggregate(context.Background(), db, ids["nested"])
	if err != nil {
		t.Fatal(err)
	}
	content(t, nested.Subtree, "1", "0", "0", "9007199254740993", "0", "9007199254740992", "0")
	second, err := index.ReadDirectoryAggregate(context.Background(), db, ids["root-b"])
	if err != nil {
		t.Fatal(err)
	}
	content(t, second.Subtree, "3", "0", "0", "18", "1", "24", "1")
	// Classification remains independent of which metric is unavailable.
	secondDocuments := second.Subtree.Types["document"]
	if string(secondDocuments.FileCount) != "2" {
		t.Fatal(secondDocuments)
	}
	metric(t, secondDocuments.Logical, "11", "1")
	metric(t, secondDocuments.Allocated, "16", "1")
	if _, err = index.ReadDirectoryAggregate(context.Background(), db, ids["direct"]); err == nil {
		t.Fatal("file accepted as directory aggregate")
	}
}

// TestAggregateLocalShapes qualifies the production aggregator at explicit, small local sizes only.
// The deep fixture tests index robustness; the production scanner's depth limit remains 256.
func TestAggregateLocalShapes(t *testing.T) {
	for _, shape := range []string{"wide-10000", "deep-255"} {
		t.Run(shape, func(t *testing.T) {
			db := qualificationDB(t)
			nodes := []contracts.Node{qualificationNode("root", "", "root", "directory", "?", "?", "volume")}
			if shape == "wide-10000" {
				for n := 0; n < 10000; n++ {
					id := fmt.Sprintf("f%d", n)
					nodes = append(nodes, qualificationNode(id, "root", id+".txt", "file", "3", "4096", "volume"))
				}
			} else {
				parent := "root"
				for n := 0; n < 255; n++ {
					dir := fmt.Sprintf("d%d", n)
					file := fmt.Sprintf("f%d", n)
					nodes = append(nodes, qualificationNode(dir, parent, dir, "directory", "?", "?", "volume"), qualificationNode(file, dir, file+".txt", "file", "3", "4096", "volume"))
					parent = dir
				}
			}
			ids := insertQualificationNodes(t, db, nodes)
			nodes = nil
			runtime.GC()
			ctx, cancel := context.WithTimeout(context.Background(), qualificationTimeout)
			defer cancel()
			// Sample Go heap only during aggregation; this is not a process RSS or native-memory guarantee.
			var before runtime.MemStats
			runtime.ReadMemStats(&before)
			peak := before.HeapAlloc
			stop := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case <-ticker.C:
						var m runtime.MemStats
						runtime.ReadMemStats(&m)
						if m.HeapAlloc > peak {
							peak = m.HeapAlloc
						}
					}
				}
			}()
			started := time.Now()
			err := index.BuildDerivedProjections(ctx, db, index.DefaultFileTypeMapping())
			elapsed := time.Since(started)
			close(stop)
			wg.Wait()
			if err != nil {
				t.Fatal(err)
			}
			root, err := index.ReadDirectoryAggregate(ctx, db, ids["root"])
			if err != nil {
				t.Fatal(err)
			}
			if shape == "wide-10000" {
				content(t, root.Direct, "10000", "0", "0", "30000", "0", "40960000", "0")
				content(t, root.Subtree, "10000", "0", "0", "30000", "0", "40960000", "0")
			} else {
				content(t, root.Direct, "0", "1", "0", "0", "0", "0", "0")
				content(t, root.Subtree, "255", "255", "0", "765", "0", "1044480", "0")
				halfway, e := index.ReadDirectoryAggregate(ctx, db, ids["d127"])
				if e != nil {
					t.Fatal(e)
				}
				content(t, halfway.Subtree, "128", "127", "0", "384", "0", "524288", "0")
				leaf, e := index.ReadDirectoryAggregate(ctx, db, ids["d254"])
				if e != nil {
					t.Fatal(e)
				}
				content(t, leaf.Subtree, "1", "0", "0", "3", "0", "4096", "0")
			}
			var databaseBytes int64
			if list, e := db.Query(`PRAGMA database_list`); e == nil {
				for list.Next() {
					var n int
					var name, path string
					if list.Scan(&n, &name, &path) == nil && name == "main" {
						if info, e := os.Stat(path); e == nil {
							databaseBytes = info.Size()
						}
					}
				}
				list.Close()
			}
			t.Logf("aggregate qualification shape=%s elapsed=%s sampledGoHeapPeakBytes=%d baselineGoHeapBytes=%d databaseBytes=%d platform=%s/%s instrumentation=%s", shape, elapsed, peak, before.HeapAlloc, databaseBytes, runtime.GOOS, runtime.GOARCH, qualificationInstrumentation)
		})
	}
}
