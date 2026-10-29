package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"strconv"
)

// TestAggregateDepthBatches crosses several 64-directory boundaries and verifies independent tree totals.
func TestAggregateDepthBatches(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = InitializeGeneration(ctx, db, "snapshot"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	w, err := NewNodeWriter(ctx, tx, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rid, err := w.AddRoot("volume", "volume", nil)
	if err != nil {
		t.Fatal(err)
	}
	put := func(path, parent, kind string, size *contracts.Bytes) string {
		t.Helper()
		var pid int64
		if parent != "" {
			pid, _ = strconv.ParseInt(parent, 10, 64)
		}
		id, e := w.Insert(rid, pid, []byte(filepath.Base(path)), kind, size, nil, "identity", nil)
		if e != nil {
			t.Fatal(e)
		}
		return strconv.FormatInt(id, 10)
	}
	root := put(".", "", "directory", nil)
	var dirs []string
	for i := 1; i <= 130; i++ {
		path := fmt.Sprintf("d%03d", i)
		d := put(path, root, "directory", nil)
		dirs = append(dirs, d)
		sub := put(path+"/sub", d, "directory", nil)
		size := contracts.Bytes(fmt.Sprint(i))
		put(path+"/sub/file.txt", sub, "file", &size)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = BuildDerivedProjections(ctx, db, DefaultFileTypeMapping()); err != nil {
		t.Fatal(err)
	}
	a, err := ReadDirectoryAggregate(ctx, db, root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Direct.DirectoryCount != "130" || a.Subtree.DirectoryCount != "260" || a.Subtree.FileCount != "130" || a.Subtree.Logical.KnownBytes != "8515" || a.Subtree.Allocated.UnknownFileCount != "130" {
		t.Fatal(a)
	}
	for i, id := range dirs {
		a, e := ReadDirectoryAggregate(ctx, db, id)
		if e != nil || a.Subtree.Logical.KnownBytes != contracts.Bytes(fmt.Sprint(i+1)) || a.Subtree.FileCount != "1" {
			t.Fatal(i, a, e)
		}
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = validateAggregates(ctx, tx, DefaultFileTypeMapping()); err != nil {
		t.Fatal(err)
	}
	// Persisted corruption must still fail the independent batched recurrence check.
	if _, err = tx.Exec(`UPDATE directory_aggregates SET aggregate_json=json_set(aggregate_json,'$.subtree.logical.knownBytes','999') WHERE node_id=?`, dirs[65]); err != nil {
		t.Fatal(err)
	}
	if validateAggregates(ctx, tx, DefaultFileTypeMapping()) == nil {
		t.Fatal("corrupted batch boundary accepted")
	}
}
