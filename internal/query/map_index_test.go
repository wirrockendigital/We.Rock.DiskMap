package query

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/mapindex"
)

// TestMapIndexSourcePartition covers native wrappers and the remaining volume areas without duplication.
func TestMapIndexSourcePartition(t *testing.T) {
	db, h, _ := sourceTopologyFixture(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := BuildMapIndex(ctx, db, h, dir); err != nil {
		t.Fatal(err)
	}
	r, err := mapindex.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root, err := r.Slot(ctx, virtualID(h.SnapshotID, "nas", ""))
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Record(root)
	if err != nil {
		t.Fatal(err)
	}
	total, err := n.Total()
	if err != nil {
		t.Fatal(err)
	}
	var want string
	if err = db.QueryRow(`SELECT CAST(SUM(CAST(logical_bytes AS INTEGER)) AS TEXT) FROM nodes WHERE kind='file'`).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if total.String() != want {
		t.Fatalf("source partition total %s != %s", total.String(), want)
	}
	seen := map[uint64]bool{}
	pending := []uint64{root}
	for len(pending) > 0 {
		slot := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[slot] {
			t.Fatal("repeated partition")
		}
		seen[slot] = true
		n, e := r.Record(slot)
		if e != nil {
			t.Fatal(e)
		}
		if n.Left != 0 {
			pending = append(pending, n.Left)
		}
		if n.Right != 0 {
			pending = append(pending, n.Right)
		}
	}
	if uint64(len(seen)) != r.Count {
		t.Fatalf("unreachable records %d of %d", len(seen), r.Count)
	}
}

// TestMapIndexWide verifies a disk-backed layout keeps every real file addressable.
func TestMapIndexWide(t *testing.T) {
	db, h, _ := fixture(t, "wide", 3000, "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := BuildMapIndex(context.Background(), db, h, dir); err != nil {
		t.Fatal(err)
	}
	r, e := mapindex.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	var id string
	if e = db.QueryRow(`SELECT CAST(id AS TEXT) FROM nodes WHERE kind='file' ORDER BY id DESC LIMIT 1`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Slot(context.Background(), id); e != nil {
		t.Fatal(e)
	}
}

// BuildMapIndex is a test-only adapter; production filesystem ownership remains in jobs.
func BuildMapIndex(ctx context.Context, db *sql.DB, h index.GenerationHeader, dir string) error {
	b, err := mapindex.NewBuilder(ctx, dir)
	if err != nil {
		return err
	}
	defer b.Close()
	if err = FeedMapIndex(ctx, db, h, b); err != nil {
		return err
	}
	if err = b.Finish(); err != nil {
		return err
	}
	return mapindex.SealBlocks(context.Background(), dir)
}
