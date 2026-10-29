package query

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Independent sorted names prove that native batches preserve sparse matches and complete global ordering.
func TestSearchProductivePilotOracle(t *testing.T) {
	names := []string{}
	db, h, _ := fixture(t, "wide", 1700, t.TempDir(), func(db *sql.DB) {
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for row := 2; row <= 1700; row++ {
			name := fmt.Sprintf("%05d", row)
			if row%12 == 0 {
				name += "-hot"
			}
			// Sparse hits span many native batches before filling the first page.
			if row%24 == 0 {
				name += "-sparse"
			}
			if row <= 21 || row > 1400 {
				name += "-gap"
			}
			names = append(names, name)
			if _, e = tx.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, []byte(name), row); e != nil {
				t.Fatal(e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	})
	for _, term := range []string{"hot", "gap", "sparse"} {
		for _, direction := range []string{"asc", "desc"} {
			for _, scope := range []string{"direct", "recursive"} {
				t.Run(term+"/"+direction+"/"+scope, func(t *testing.T) {
					want := []string{}
					for _, name := range names {
						if strings.Contains(name, term) {
							want = append(want, name)
						}
					}
					sort.Strings(want)
					if direction == "desc" {
						sort.Sort(sort.Reverse(sort.StringSlice(want)))
					}
					r := baseSearch(h)
					r.Text = term
					r.Direction = direction
					r.Scope = scope
					got := []string{}
					cursor := ""
					for page := 0; page < 20; page++ {
						out, e := Search(context.Background(), db, h, r, cursor)
						if e != nil || out.WorkLimited {
							t.Fatalf("page=%d error=%v limited=%v", page, e, out.WorkLimited)
						}
						for _, item := range out.Items {
							got = append(got, item.Name)
						}
						if !out.HasMore {
							break
						}
						if len(out.Items) != 50 || out.NextCursor == "" || out.NextCursor == cursor {
							t.Fatal("invalid full page")
						}
						cursor = out.NextCursor
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("ordered oracle mismatch: got %d want %d", len(got), len(want))
					}
				})
			}
		}
	}
	// An already cancelled request cannot publish a prefix collected by any plan.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := baseSearch(h)
	r.Text = "hot"
	if out, e := Search(ctx, db, h, r, ""); e == nil || len(out.Items) != 0 {
		t.Fatal("cancelled query published data", e)
	}
}
