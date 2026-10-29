package query

import (
	"context"
	"database/sql"
	"math/big"
	"sort"
	"testing"
)

// The complete paginated result must match an independent full sort, including nulls and positive numeric ties.
func TestNativeSizeBoundGlobalPages(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		_, err := db.Exec(`UPDATE nodes SET allocated_bytes=CASE WHEN rowid%7=0 THEN NULL ELSE CAST(rowid%11 AS TEXT) END WHERE kind='file'; UPDATE nodes SET rowid=9223372036854775807 WHERE rowid=4`)
		if err != nil {
			t.Fatal(err)
		}
	})
	type expected struct {
		id   string
		row  int64
		size sql.NullString
	}
	for _, metric := range []string{"logical", "allocated"} {
		for _, direction := range []string{"asc", "desc"} {
			rows, err := db.Query(`SELECT id,rowid,` + metric + `_bytes FROM nodes WHERE kind='file'`)
			if err != nil {
				t.Fatal(err)
			}
			want := []expected{}
			for rows.Next() {
				var v expected
				if err = rows.Scan(&v.id, &v.row, &v.size); err != nil {
					t.Fatal(err)
				}
				want = append(want, v)
			}
			if err = rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			sort.Slice(want, func(i, j int) bool {
				a, b := want[i], want[j]
				cmp := 0
				if a.size.Valid != b.size.Valid {
					if a.size.Valid {
						cmp = 1
					} else {
						cmp = -1
					}
				} else if a.size.Valid {
					x, _ := new(big.Int).SetString(a.size.String, 10)
					y, _ := new(big.Int).SetString(b.size.String, 10)
					cmp = x.Cmp(y)
				}
				if cmp == 0 {
					if a.row < b.row {
						cmp = -1
					} else if a.row > b.row {
						cmp = 1
					}
				}
				if direction == "desc" {
					return cmp > 0
				}
				return cmp < 0
			})
			r := baseSearch(h)
			r.Sort = "size"
			r.Metric = metric
			r.Direction = direction
			r.Text = "n"
			r.Field = "name"
			cursor := ""
			offset := 0
			for page := 0; page < 20; page++ {
				out, e := Search(context.Background(), db, h, r, cursor)
				if e != nil || out.WorkLimited {
					t.Fatalf("%s/%s: %v", metric, direction, e)
				}
				for _, item := range out.Items {
					if offset >= len(want) || item.NodeID != want[offset].id {
						t.Fatalf("%s/%s mismatch at %d", metric, direction, offset)
					}
					offset++
				}
				if !out.HasMore {
					break
				}
				if out.NextCursor == "" || out.NextCursor == cursor {
					t.Fatal("cursor did not advance")
				}
				cursor = out.NextCursor
			}
			if offset != len(want) {
				t.Fatalf("got %d want %d", offset, len(want))
			}
		}
	}
}
