package index

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
)

// Numeric keyset iteration preserves bounded pages and stops on cancellation or visitor failure.
func TestDirectoryPagesCancellation(t *testing.T) {
	db, tx, w, rid, root := numericFixture(t)
	for i := 0; i < 600; i++ {
		name := []byte{byte('a' + i%26), byte('a' + i/26)}
		if _, e := w.Insert(rid, root, name, "file", nil, nil, "identity", nil); e != nil {
			t.Fatal(e)
		}
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	for _, cancelled := range []bool{false, true} {
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithCancel(context.Background())
		pages := 0
		sentinel := errors.New("visitor failed")
		e = walkDirectoryRows(ctx, tx, false, func([]directoryRow) error {
			pages++
			if cancelled {
				cancel()
				return nil
			}
			return sentinel
		})
		cancel()
		tx.Rollback()
		want := sentinel
		if cancelled {
			want = context.Canceled
		}
		if !errors.Is(e, want) || pages != 1 {
			t.Fatal(e, pages)
		}
	}
}

// Paging uses the integer primary key directly, not an OFFSET or random text-ID index.
func TestDirectoryPageQueryPlan(t *testing.T) {
	db, _ := generationFixture(t)
	rows, e := db.Query(`EXPLAIN QUERY PLAN SELECT n.id,n.kind FROM nodes n WHERE n.id>? ORDER BY n.id LIMIT 256`, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	var detail string
	for rows.Next() {
		var a, b, c int
		if e = rows.Scan(&a, &b, &c, &detail); e != nil {
			t.Fatal(e)
		}
	}
	if detail != "SEARCH n USING INTEGER PRIMARY KEY (rowid>?)" {
		t.Fatal(detail)
	}
}

// Sparse directories across many file rows must each appear once, including empty directories.
func TestDirectoryPagesSparseCoverage(t *testing.T) {
	db, tx, writer, rid, root := numericFixture(t)
	want := map[string]bool{strconv.FormatInt(root, 10): true}
	for i := 0; i < 140; i++ {
		for f := 0; f < 17; f++ {
			if _, err := writer.Insert(rid, root, []byte(fmt.Sprintf("file-%d-%d", i, f)), "file", nil, nil, "identity", nil); err != nil {
				t.Fatal(err)
			}
		}
		id, err := writer.Insert(rid, root, []byte(fmt.Sprintf("empty-%d", i)), "directory", nil, nil, "identity", nil)
		if err != nil {
			t.Fatal(err)
		}
		want[strconv.FormatInt(id, 10)] = true
	}
	writer.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, aggregates := range []bool{false, true} {
		read, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		err = walkDirectoryRows(context.Background(), read, aggregates, func(page []directoryRow) error {
			for _, row := range page {
				if seen[row.id] || !want[row.id] {
					t.Fatalf("duplicate or unexpected directory %s", row.id)
				}
				if row.raw.Valid {
					t.Fatal("missing aggregate must remain observable")
				}
				seen[row.id] = true
			}
			return nil
		})
		read.Rollback()
		if err != nil || len(seen) != len(want) {
			t.Fatalf("coverage: %d/%d, %v", len(seen), len(want), err)
		}
	}
}
