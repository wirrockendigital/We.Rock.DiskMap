package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"
)

// TestHierarchyAdoptsDecodedBatch preserves useful rows when the optional clock
// expires inside either the first batch or a later batch, including its lookahead.
func TestHierarchyAdoptsDecodedBatch(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	for _, cutoff := range []int{10, 130, 150, 270} {
		t.Run(fmt.Sprint(cutoff), func(t *testing.T) {
			base := time.Unix(0, 0)
			calls := 0
			now := func() time.Time {
				calls++
				if calls >= cutoff {
					return base.Add(ExpansionBudget)
				}
				return base
			}
			r := Request{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Depth: 8, ElementBudget: 1000}
			out, err := hierarchy(context.Background(), db, h, r, now)
			if err != nil {
				t.Fatal(err)
			}
			conserved(t, out)
			seen := map[string]bool{}
			var group Node
			for _, n := range out.Nodes {
				if n.Kind == "group" {
					group = n
				} else {
					seen[n.ID] = true
				}
			}
			if group.GroupReason != "time-budget" || len(seen) < 2 {
				t.Fatal("decoded useful rows lost", len(seen), group.GroupReason)
			}
			if cutoff > 140 && len(seen) <= 129 {
				t.Fatal("no progress beyond first batch", len(seen))
			}
			// Consume the exact suffix through the public continuation API; no warmed
			// rerun of the original request is needed to recover a discarded prefix.
			r.RootID = group.ListRootID
			page, err := Children(context.Background(), db, h, r, group.AfterCursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range page.Nodes {
				if seen[n.ID] {
					t.Fatal("duplicate across batch/cursor", n.ID)
				}
				seen[n.ID] = true
			}
			if page.HasMore || len(seen) != 1000 {
				t.Fatal("lost lookahead or suffix", len(seen), page.HasMore)
			}
		})
	}
}

// TestHierarchyBatchEmptyFilteredPrefix verifies that zero emitted matches do
// not terminate physical paging before later matching children are reached.
func TestHierarchyBatchEmptyFilteredPrefix(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	f := Filter{Text: "n999", Field: "name"}
	projection := projectionFixture(t, db, h, "logical", f)
	r := Request{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Depth: 1, ElementBudget: 1000, Filter: f, Mode: "hits-only"}
	out, err := hierarchyProjection(context.Background(), db, projection, h, r, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, out)
	if len(out.Nodes) != 2 || out.Nodes[1].Name != "n999" || out.Truncated {
		t.Fatalf("filtered physical prefix did not progress: %+v", out)
	}
}

// TestHierarchyBatchSignedLookahead checks batch boundaries independently against
// SQLite ordering, including sparse positive and maximum numeric continuation keys.
func TestHierarchyBatchSignedLookahead(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		for i, row := range []int64{10001, 10002, math.MaxInt64} {
			if _, err := db.Exec(`UPDATE nodes SET rowid=? WHERE rowid=?`, row, i+2); err != nil {
				t.Fatal(err)
			}
		}
	})
	root := fixtureNodeID(h.SnapshotID, "volume", ".")
	rows, err := db.Query(`SELECT id FROM nodes WHERE parent_id=? ORDER BY rowid`, root)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		want = append(want, id)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, budget := range []int{130, 131, 258, 259, 1001} {
		r := Request{SnapshotID: h.SnapshotID, RootID: root, Metric: "logical", Depth: 1, ElementBudget: budget}
		out, err := hierarchy(context.Background(), db, h, r, func() time.Time { return time.Unix(0, 0) })
		if err != nil {
			t.Fatal(err)
		}
		conserved(t, out)
		var got []string
		var group Node
		for _, n := range out.Nodes {
			if n.Kind == "group" {
				group = n
			} else if n.ID != root {
				got = append(got, n.ID)
			}
		}
		if group.ID != "" {
			r.ElementBudget = 1000
			page, err := Children(context.Background(), db, h, r, group.AfterCursor)
			if err != nil {
				t.Fatal(err)
			}
			if page.HasMore {
				t.Fatal("unexpected suffix truncation")
			}
			for _, n := range page.Nodes {
				got = append(got, n.ID)
			}
		}
		if len(got) != len(want) {
			t.Fatal(budget, "count", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal(budget, "row order or lookahead lost", i)
			}
		}
	}
}
