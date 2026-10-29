package jobs

import (
	"context"
	"database/sql"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// fixtureNodes reads compact test generations through the production bounded reader.
func fixtureNodes(t *testing.T, db *sql.DB) []index.NodeRecord {
	t.Helper()
	reader, err := index.NewNodeReader(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	out := []index.NodeRecord{}
	var after int64
	for {
		page, err := reader.Page(after, 128)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, page...)
		if len(page) < 128 {
			return out
		}
		after = page[len(page)-1].RowID
	}
}
