package index

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// numericFixture creates a canonical root in one caller-controlled scan transaction.
func numericFixture(t testing.TB) (*sql.DB, *sql.Tx, *NodeWriter, int64, int64) {
	t.Helper()
	db, e := Open(filepath.Join(t.TempDir(), "numeric.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if e = InitializeGeneration(ctx, db, "snapshot"); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { tx.Rollback() })
	w, e := NewNodeWriter(ctx, tx, "snapshot")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	rid, e := w.AddRoot("volume", "volume", nil)
	if e != nil {
		t.Fatal(e)
	}
	root, e := w.Insert(rid, 0, []byte("volume"), "directory", nil, nil, "identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	return db, tx, w, rid, root
}

// The physical schema contains neither full paths nor repeated source or snapshot metadata.
func TestCompactPhysicalSchemaAndDisplay(t *testing.T) {
	db, tx, w, rid, root := numericFixture(t)
	name := []byte("bad\xff-name")
	size := contracts.Bytes("18446744073709551616")
	id, e := w.Insert(rid, root, name, "file", &size, nil, "private-identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'search_%'`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
	if e = db.QueryRow(`SELECT count(*) FROM pragma_table_info('nodes') WHERE name IN ('node_json','path_bytes','snapshot_id','target_id','volume_id','share_id','modified_at','owner_id','created_at','link_group_id')`).Scan(&count); e != nil || count != 0 {
		t.Fatal("redundant columns", count, e)
	}
	got, e := ReadNode(context.Background(), db, strconv.FormatInt(id, 10))
	if e != nil {
		t.Fatal(e)
	}
	if got.Node.OriginalName != strconv.QuoteToASCII(string(name)) || string(got.Path) != string(name) || got.Node.LogicalBytes == nil || *got.Node.LogicalBytes != size {
		t.Fatal(got)
	}
	found, e := FindNode(context.Background(), db, "volume", name)
	if e != nil || found.RowID != id {
		t.Fatal(found, e)
	}
	if _, e = ReadNode(context.Background(), db, "nas"); e != sql.ErrNoRows {
		t.Fatal("virtual lookup", e)
	}
}

// Numeric IDs cannot be signed, noncanonical, outside the integer range or old hash locators.
func TestNumericLocatorContract(t *testing.T) {
	for _, id := range []string{"0", "-1", "01", "+1", "9223372036854775808", "abcdef"} {
		if _, e := NumericNodeID(id); e == nil {
			t.Fatal(id)
		}
	}
	if _, e := NumericNodeID("9223372036854775807"); e != nil {
		t.Fatal(e)
	}
}

// Rolling back the scan transaction removes both source membership and every inserted node.
func TestCompactInsertRollback(t *testing.T) {
	db, tx, _, _, _ := numericFixture(t)
	if e := tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"nodes", "roots"} {
		var count int
		if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); e != nil || count != 0 {
			t.Fatal(table, count, e)
		}
	}
}

// Cached reconstruction is bounded across pages, preserves raw paths and checks every parent root.
func TestNumericReaderPagesAndBounds(t *testing.T) {
	db, tx, w, rid, root := numericFixture(t)
	for i := 0; i < 300; i++ {
		dir, e := w.Insert(rid, root, []byte(fmt.Sprint("dir", i)), "directory", nil, nil, "identity", nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Insert(rid, dir, []byte("file"), "file", nil, nil, "identity", nil); e != nil {
			t.Fatal(e)
		}
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	r, e := NewNodeReader(context.Background(), db)
	if e != nil {
		t.Fatal(e)
	}
	var cursor int64
	count := 0
	for {
		page, e := r.Page(cursor, 128)
		if e != nil {
			t.Fatal(e)
		}
		if len(page) == 0 {
			break
		}
		for _, v := range page {
			if v.Node.Kind == "file" && len(v.Path) < 8 {
				t.Fatal(v)
			}
			cursor = v.RowID
			count++
		}
		if len(r.paths) > 256 || r.pathBytes > 1<<20 {
			t.Fatal("unbounded cache")
		}
	}
	if count != 601 {
		t.Fatal(count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = NewNodeReader(ctx, db); e == nil {
		t.Fatal("cancel ignored")
	}
}

// A node cannot borrow an ancestor from another captured root, even with valid numeric locators.
func TestNumericReaderRejectsCrossRoot(t *testing.T) {
	db, tx, w, rid, root := numericFixture(t)
	other, e := w.AddRoot("other", "other", nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.Insert(other, 0, []byte("other"), "directory", nil, nil, "identity", nil); e != nil {
		t.Fatal(e)
	}
	child, e := w.Insert(rid, root, []byte("file"), "file", nil, nil, "identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE nodes SET root_id=? WHERE id=?`, other, child); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadNode(context.Background(), db, strconv.FormatInt(child, 10)); e == nil {
		t.Fatal("foreign ancestor accepted")
	}
	if _, e = FindNode(context.Background(), db, "volume", []byte("file")); e == nil {
		t.Fatal("foreign path accepted")
	}
}

// Reader traversal has a fixed depth ceiling even for structurally acyclic numeric trees.
func TestNumericReaderDepthLimit(t *testing.T) {
	db, tx, w, rid, parent := numericFixture(t)
	for i := 0; i < 257; i++ {
		var e error
		parent, e = w.Insert(rid, parent, []byte("d"), "directory", nil, nil, "identity", nil)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadNode(context.Background(), db, strconv.FormatInt(parent, 10)); e == nil {
		t.Fatal("excessive depth accepted")
	}
}

// A finalized source-membership table is protected by the same immutable-generation triggers as nodes.
func TestNumericRootsSeal(t *testing.T) {
	db, h := generationFixture(t)
	if e := FinalizeGeneration(context.Background(), db, h); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{`UPDATE roots SET volume_id='other'`, `DELETE FROM roots`, `INSERT INTO roots(target_id,volume_id) VALUES('other','other')`} {
		if _, e := db.Exec(statement); e == nil {
			t.Fatal("sealed root mutation", statement)
		}
	}
}

// API callers receive owned path bytes and cannot mutate the reader's cached ancestry.
func TestNumericReaderPathOwnership(t *testing.T) {
	db, tx, w, rid, parent := numericFixture(t)
	dir, e := w.Insert(rid, parent, []byte("dir"), "directory", nil, nil, "identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	file, e := w.Insert(rid, dir, []byte("file"), "file", nil, nil, "identity", nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	reader, e := NewNodeReader(context.Background(), db)
	if e != nil {
		t.Fatal(e)
	}
	displayed, e := reader.Read(strconv.FormatInt(dir, 10))
	if e != nil {
		t.Fatal(e)
	}
	displayed.Path[0] = 'x'
	selected, e := reader.Read(strconv.FormatInt(file, 10))
	if e != nil || string(selected.Path) != "dir/file" {
		t.Fatal(selected, e)
	}
}

// The deepest supported directory can still own a terminal file without allowing deeper directories.
func TestNumericReaderDeepestLeaf(t *testing.T) {
	db, tx, w, rid, parent := numericFixture(t)
	for i := 0; i < 256; i++ {
		var err error
		parent, err = w.Insert(rid, parent, []byte("d"), "directory", nil, nil, "identity", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	leaf, err := w.Insert(rid, parent, []byte("file"), "file", nil, nil, "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expected := strings.Repeat("d/", 256) + "file"
	node, err := ReadNode(context.Background(), db, strconv.FormatInt(leaf, 10))
	if err != nil || string(node.Path) != expected {
		t.Fatal(node, err)
	}
	found, err := FindNode(context.Background(), db, "volume", []byte(expected))
	if err != nil || found.RowID != leaf {
		t.Fatal(found, err)
	}
	reader, err := NewNodeReader(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Read(strconv.FormatInt(parent, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Read(strconv.FormatInt(leaf, 10)); err != nil {
		t.Fatal("cached ancestry", err)
	}
}
