package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
)

// TestSignedRowContinuation seals a generation with the positive numeric locator range before paging it.
func TestSignedRowContinuation(t *testing.T) {
	rows := []int64{10001, 10002, math.MaxInt64}
	db, h, oracle := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		for i, row := range rows {
			if _, err := db.Exec(`UPDATE nodes SET rowid=? WHERE rowid=?`, row, i+2); err != nil {
				t.Fatal(err)
			}
		}
	})
	r := Request{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Depth: 1, ElementBudget: 1}
	r, _ = r.Validate()
	seen := map[string]bool{}
	c := ""
	// The independent ordering oracle includes all 999 children and sparse final numeric locators.
	expectedRows := make([]int64, 996)
	for i := range expectedRows {
		expectedRows[i] = int64(i + 5)
	}
	expectedRows = append(expectedRows, rows...)

	for i, want := range expectedRows {
		p, err := Children(context.Background(), db, h, r, c)
		if err != nil || len(p.Nodes) != 1 {
			t.Fatal(i, p, err)
		}
		if seen[p.Nodes[0].ID] {
			t.Fatal("duplicate row")
		}
		seen[p.Nodes[0].ID] = true
		if p.HasMore != (i < len(expectedRows)-1) {
			t.Fatal("incorrect continuation", i, p)
		}
		if p.HasMore {
			got, err := parseCursor(r, p.NextCursor)
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
			c = p.NextCursor
		}
	}
	// A group after any sparse numeric row must retain a nonempty, selector-bound cursor.
	for _, budget := range []int{3, 4, 5} {
		r.ElementBudget = budget
		out, err := Hierarchy(context.Background(), db, h, r)
		if err != nil {
			t.Fatal(err)
		}
		conserved(t, out)
		if string(out.Nodes[0].Summary.KnownBytes) != oracle.KnownSum || string(out.Nodes[0].Summary.UnknownFileCount) != fmt.Sprint(oracle.Unknown) {
			t.Fatal("oracle mismatch", out)
		}
		for _, n := range out.Nodes {
			if n.Kind == "group" {
				if n.AfterCursor == "" {
					t.Fatal("lost signed continuation")
				}
				p, err := Children(context.Background(), db, h, r, n.AfterCursor)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Nodes) != budget {
					t.Fatal("missing residual row", p)
				}
				var firstRow int64
				if err := db.QueryRow(`SELECT rowid FROM nodes WHERE id=?`, p.Nodes[0].ID).Scan(&firstRow); err != nil {
					t.Fatal(err)
				}
				if firstRow != expectedRows[budget-2] {
					t.Fatal("group skipped or repeated a child", firstRow, expectedRows[budget-2])
				}
			}
		}
	}
	// Every positive boundary is a valid canonical cursor; noncanonical numeric spellings are rejected.
	for _, row := range rows {
		v, err := parseCursor(r, cursor(r, row))
		if err != nil || v != row {
			t.Fatal(row, v, err)
		}
	}
}
