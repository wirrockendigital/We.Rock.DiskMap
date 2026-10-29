package query

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// A selective term beyond 4096 source rows must remain visible in both globally ordered pages.
func TestSearchIntermediatePostingSet(t *testing.T) {
	db, h, _ := fixture(t, "wide", 8300, t.TempDir(), func(db *sql.DB) {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for row := 2; row <= 8300; row++ {
			prefix := "other"
			if row > 4100 {
				prefix = "n999"
			}
			name := fmt.Sprintf("%s%05d", prefix, row)
			if _, err = tx.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, []byte(name), row); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	r := baseSearch(h)
	r.Text = "n999"
	for _, direction := range []string{"asc", "desc"} {
		r.Direction = direction
		seen := map[string]bool{}
		cursor := ""
		for page := 0; page < 2; page++ {
			result, err := Search(context.Background(), db, h, r, cursor)
			if err != nil || len(result.Items) != 50 || result.WorkLimited {
				t.Fatalf("direction=%s page=%d items=%d limited=%v err=%v", direction, page, len(result.Items), result.WorkLimited, err)
			}
			for _, item := range result.Items {
				if seen[item.NodeID] {
					t.Fatal("duplicate")
				}
				seen[item.NodeID] = true
			}
			cursor = result.NextCursor
		}
	}
}
