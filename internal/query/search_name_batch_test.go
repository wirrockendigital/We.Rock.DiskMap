package query

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Sparse name matching must cross empty and exact physical page boundaries while
// retaining the same canonical Unicode/invalid-byte results as reconstructed nodes.
func TestNamePrefilterPhysicalPages(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1025, t.TempDir(), func(db *sql.DB) {
		for id, name := range map[int][]byte{129: []byte("CoDeX"), 256: []byte("codex-Σ"), 1025: {0xff, 'x'}} {
			if _, err := db.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, name, id); err != nil {
				t.Fatal(err)
			}
		}
	})
	ctx := context.Background()
	r := baseSearch(h)
	r.Field = "name"
	for _, term := range []string{"codex", "σ", "\\xff", "not-present", "n"} {
		r.Text = term
		needle := index.SearchFold(term)
		original, err := index.NewNodeReader(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{}
		var last int64
		for {
			records, err := original.Page(last, 128)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range records {
				last = v.RowID
				if nativeLiteralContains(v.Name, needle) {
					want = append(want, v.RowID)
				}
			}
			if len(records) < 128 {
				break
			}
		}
		filtered, err := index.NewNodeReader(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		got := []int64{}
		last = 0
		pages := 0
		for {
			records, next, done, err := searchReadBatch(ctx, db, filtered, r, needle, last)
			if err != nil {
				t.Fatal(err)
			}
			pages++
			if next <= last && !done {
				t.Fatal("physical cursor stalled")
			}
			last = next
			for _, v := range records {
				if nativeLiteralContains(v.Name, needle) {
					got = append(got, v.RowID)
				}
			}
			if done {
				break
			}
			if pages > 9 {
				t.Fatal("unbounded pages")
			}
		}
		if pages != 9 || !reflect.DeepEqual(got, want) {
			t.Fatalf("term=%q pages=%d got=%v want=%v", term, pages, got, want)
		}
	}
}
