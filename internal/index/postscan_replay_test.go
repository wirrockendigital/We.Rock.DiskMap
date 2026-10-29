package index

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestPostscanReplay operates exclusively on an explicitly supplied disposable metadata copy.
// It is opt-in so normal tests never open a live application database.
func TestPostscanReplay(t *testing.T) {
	file := os.Getenv("DISKMAP_POSTSCAN_COPY")
	if file == "" {
		t.Skip("isolated replay not requested")
	}
	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err = db.QueryRow(`SELECT header_json FROM generation_header`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var h GenerationHeader
	if err = json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='trigger'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var s string
		if err = rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		names = append(names, s)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	// Drop only known generated seal triggers in the caller-owned replay copy.
	for _, name := range names {
		if _, err = db.Exec(`DROP TRIGGER "` + name + `"`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`DELETE FROM generation_header; DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping; PRAGMA cache_size=-2048; PRAGMA mmap_size=0`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now()
	if err = BuildDerivedProjectionsWithCache(ctx, db, h.FileTypeMapping, 1024); err != nil {
		t.Fatal(err)
	}
	t.Logf("aggregateSeconds=%.3f", time.Since(start).Seconds())
	start = time.Now()
	if err = FinalizeGeneration(ctx, db, h); err != nil {
		t.Fatal(err)
	}
	t.Logf("validationSeconds=%.3f entries=%d", time.Since(start).Seconds(), h.Entries)
}
