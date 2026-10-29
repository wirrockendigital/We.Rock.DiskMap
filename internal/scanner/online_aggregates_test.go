package scanner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Real traversal must produce the same exact aggregates as the independent batch builder.
func TestScanOnlineAggregatesMatchBatch(t *testing.T) {
	p, manifest := coverageFixture(t)
	for _, root := range manifest.Roots {
		if err := os.MkdirAll(filepath.Join(root.PhysicalPath, "nested", "empty"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root.PhysicalPath, "nested", "photo.custom"), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	mapping := index.DefaultFileTypeMapping()
	mapping.Assignments = append(mapping.Assignments, contracts.ExtensionMapping{Extension: "custom", Category: "image"})
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = ScanManifest(context.Background(), db, "online", p, manifest, mapping); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]string {
		rows, err := db.Query(`SELECT node_id,aggregate_json FROM directory_aggregates`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var id, raw string
			if err := rows.Scan(&id, &raw); err != nil {
				t.Fatal(err)
			}
			var a contracts.DirectoryAggregate
			if err := json.Unmarshal([]byte(raw), &a); err != nil {
				t.Fatal(err)
			}
			canonical, _ := json.Marshal(a)
			out[id] = string(canonical)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	online := read()
	if len(online) != 6 {
		t.Fatal("missing empty or nested directory aggregate", len(online))
	}
	// Rebuild only in this test to retain an independent post-scan oracle.
	if _, err := db.Exec(`DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping`); err != nil {
		t.Fatal(err)
	}
	if err := index.BuildDerivedProjections(context.Background(), db, mapping); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(online, read()) {
		t.Fatal("online aggregate differs from batch oracle")
	}
}

// Cancellation after the root insertion rolls back nodes and all online aggregate state.
func TestScanOnlineAggregateCancellationRollsBack(t *testing.T) {
	p, manifest := coverageFixture(t)
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = scanManifestWithOperations(ctx, db, "cancelled", p, manifest, index.DefaultFileTypeMapping(), walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(*os.File, int) ([]os.DirEntry, error) { cancel(); return nil, ctx.Err() }})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation accepted", err)
	}
	for _, table := range []string{"nodes", "roots", "directory_aggregates", "aggregate_mapping", "coverage"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}
