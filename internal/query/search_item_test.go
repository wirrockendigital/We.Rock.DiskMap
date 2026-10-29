package query

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"math"
	"strconv"
	"testing"
)

// TestSearchItemCanonicalRows exercises the actual decoder at positive sparse numeric locator boundaries.
func TestSearchItemCanonicalRows(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir(), func(db *sql.DB) {
		if _, err := db.Exec(`UPDATE nodes SET id=9223372036854775807 WHERE id=4`); err != nil {
			t.Fatal(err)
		}
	})
	for _, row := range []int64{2, 3, math.MaxInt64, 1} {
		var candidate searchCandidate
		candidate.row = row
		candidate.id = strconv.FormatInt(row, 10)
		stored, err := index.ReadNode(context.Background(), db, candidate.id)
		if err != nil {
			t.Fatal(err)
		}
		candidate.path = stored.Path
		node := stored.Node

		for _, metric := range []string{"logical", "allocated"} {
			request := baseSearch(h)
			request.Metric = metric
			item, err := searchItem(context.Background(), db, h, request, candidate)
			if err != nil || item.NodeID != candidate.id || item.Kind != node.Kind {
				t.Fatalf("row=%d metric=%s item=%+v err=%v", row, metric, item, err)
			}
			if node.Kind == "file" {
				expected := node.LogicalBytes
				if metric == "allocated" {
					expected = node.AllocatedBytes
				}
				// Unknown source metrics must remain null rather than becoming a fabricated zero.
				if (expected == nil) != (item.Size == nil) || (expected != nil && *item.Size != *expected) || item.Summary.FileCount != "1" {
					t.Fatalf("file summary differs: %+v", item)
				}
			}
		}
		candidate.row = row + 1
		if _, err := searchItem(context.Background(), db, h, baseSearch(h), candidate); !errors.Is(err, ErrSnapshot) {
			t.Fatalf("row/ID mismatch accepted: %v", err)
		}
	}
}
