package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// searchFixture keeps the independent size oracle while adding literal Unicode and timestamp cases.
func searchFixture(t *testing.T) (*sql.DB, index.GenerationHeader) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		for i, name := range []string{"Grüße 東京 %_ SQL ' OR 1=1 --", "Σςσ <b> & hello", "abcdef", "abc bcd cde def"} {
			row := i + 2
			if _, e := db.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, []byte(name), row); e != nil {
				t.Fatal(e)
			}
		}
	})
	return db, h
}

// baseSearch makes all selectors explicit; the same request also supports an empty literal list query.
func baseSearch(h index.GenerationHeader) SearchRequest {
	return SearchRequest{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Scope: "recursive", Field: "both", Metric: "logical", Sort: "name", Direction: "asc", Limit: 50}
}

// TestSearchLiteralUnicode exercises short literals, Unicode and SQL-like input against a sealed native index.
func TestSearchLiteralUnicode(t *testing.T) {
	db, h := searchFixture(t)
	r := baseSearch(h)
	for _, tc := range []struct {
		text  string
		count int
	}{{"grÜß", 1}, {"東京", 1}, {"ü", 1}, {"%_", 1}, {"' OR 1=1 --", 1}, {"σ", 1}, {"<b>", 1}, {"abcdef", 1}, {"missing-not-present", 0}} {
		r.Text = tc.text
		p, e := Search(context.Background(), db, h, r, "")
		if e != nil || len(p.Items) != tc.count || p.HasMore || p.WorkLimited {
			t.Fatal(tc, p, e)
		}
	}
	r.Text = ""
	r.Limit = 13
	for _, order := range []string{"name", "size", "type"} {
		for _, direction := range []string{"asc", "desc"} {
			r.Sort = order
			r.Direction = direction
			seen := map[string]bool{}
			cursor := ""
			var lastSize *big.Int
			unknownEnded := false
			for page := 0; page < 100; page++ {
				p, e := Search(context.Background(), db, h, r, cursor)
				if e != nil {
					t.Fatal(order, direction, e)
				}
				for _, item := range p.Items {
					if seen[item.NodeID] || item.SnapshotID != h.SnapshotID {
						t.Fatal("repeated/foreign node")
					}
					seen[item.NodeID] = true
					if order == "size" {
						if item.Size == nil {
							if direction == "asc" && lastSize != nil {
								t.Fatal("unknown after known ASC")
							}
							unknownEnded = true
						} else {
							if direction == "desc" && unknownEnded {
								t.Fatal("known after unknown DESC")
							}
							v, _ := new(big.Int).SetString(string(*item.Size), 10)
							if lastSize != nil && ((direction == "asc" && lastSize.Cmp(v) > 0) || (direction == "desc" && lastSize.Cmp(v) < 0)) {
								t.Fatal("non-numeric size order")
							}
							lastSize = v
						}
					}
				}
				if !p.HasMore {
					break
				}
				if p.NextCursor == "" || p.NextCursor == cursor {
					t.Fatal("stalled cursor")
				}
				cursor = p.NextCursor
			}
			if len(seen) != 999 {
				t.Fatal(order, direction, len(seen))
			}
		}
	}
}

// TestSearchScopeAndCursor rejects selector drift and separates direct children from all descendants.
func TestSearchScopeAndCursor(t *testing.T) {
	db, h, _ := fixture(t, "deep", 1000, t.TempDir())
	r := baseSearch(h)
	r.Limit = 1
	r.Scope = "direct"
	first, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(first.Items) != 1 || !first.HasMore {
		t.Fatal(first, e)
	}
	r.Text = "changed"
	if _, e = Search(context.Background(), db, h, r, first.NextCursor); !errors.Is(e, ErrSearch) {
		t.Fatal(e)
	}
	r = baseSearch(h)
	r.Text = "n1/n2"
	r.Field = "path"
	p, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(p.Items) == 0 {
		t.Fatal(p, e)
	}
	for _, item := range p.Items {
		if !strings.Contains(item.Path, "n1/n2") {
			t.Fatal(item)
		}
	}
	r.Scope = "direct"
	p, e = Search(context.Background(), db, h, r, "")
	if e != nil || len(p.Items) != 0 {
		t.Fatal(p, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = Search(ctx, db, h, r, ""); e == nil {
		t.Fatal("cancel ignored")
	}
}

// Two complete native result pages remain disjoint beyond the former physical candidate window.
func TestSearchOrderedWorkLimit(t *testing.T) {
	db, h, _ := fixture(t, "wide", 5000, t.TempDir())
	r := baseSearch(h)
	r.Text = "n"
	r.Scope = "recursive"
	r.Limit = 50
	p, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(p.Items) != 50 || !p.HasMore || p.WorkLimited {
		t.Fatal(p, e)
	}
	next, e := Search(context.Background(), db, h, r, p.NextCursor)
	if e != nil || len(next.Items) != 50 {
		t.Fatal(next, e)
	}
	seen := map[string]bool{}
	for _, item := range p.Items {
		seen[item.NodeID] = true
	}
	for _, item := range next.Items {
		if seen[item.NodeID] {
			t.Fatal("duplicate page")
		}
	}
}

// Literal matching precedes the limited sorted result window; separated substrings are not a match.
func TestSearchFalsePositivePostings(t *testing.T) {
	db, h, _ := fixture(t, "wide", 5000, t.TempDir(), func(db *sql.DB) {
		// Native numeric locators remain unchanged when names are authored before sealing.
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for i := 0; i < 4999; i++ {
			name := fmt.Sprintf("abc bcd cde %05d", i)
			if i == 4998 {
				name = "zz abcde"
			}
			if _, e = tx.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, []byte(name), i+2); e != nil {
				t.Fatal(e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	})
	r := baseSearch(h)
	r.Text = "abcde"
	first, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(first.Items) != 1 || first.HasMore || first.WorkLimited || first.Items[0].Name != "zz abcde" {
		t.Fatal(first, e)
	}
}

// Positive numeric identities remain stable global-order continuation anchors.
func TestSearchSignedCursorAndPlan(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		if _, e := db.Exec(`UPDATE nodes SET rowid=9223372036854775807 WHERE rowid=4`); e != nil {
			t.Fatal(e)
		}
	})
	r := baseSearch(h)
	r.Sort = "type"
	r.Limit = 1
	first, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(first.Items) != 1 || len(first.NextCursor) > 512 {
		t.Fatal(first, e)
	}
	_, row, e := decodeSearchCursor(r, first.NextCursor)
	if e != nil || row != 2 {
		t.Fatal(row, e)
	}
	second, e := Search(context.Background(), db, h, r, first.NextCursor)
	if e != nil || len(second.Items) != 1 {
		t.Fatal(second, e)
	}
	_, row, e = decodeSearchCursor(r, second.NextCursor)
	// Continuation identifies the last emitted positive numeric locator.
	if e != nil || row != 3 {
		t.Fatal(row, e)
	}
	_, zero, zeroErr := decodeSearchCursor(r, encodeSearchCursor(r, 0))
	if zeroErr == nil {
		t.Fatal(zero, zeroErr)
	}
	third, e := Search(context.Background(), db, h, r, second.NextCursor)
	if e != nil || len(third.Items) != 1 || third.Items[0].NodeID == second.Items[0].NodeID {
		t.Fatal(third, e)
	}
}
