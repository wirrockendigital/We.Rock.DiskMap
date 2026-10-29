package querybench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestExportBrowserHierarchy supplies real bounded query data for the separate browser memory gate.
func TestExportBrowserHierarchy(t *testing.T) {
	source, destination := os.Getenv("DISKMAP_BROWSER_SOURCE"), os.Getenv("DISKMAP_BROWSER_OUTPUT")
	if source == "" || destination == "" {
		t.Skip("explicit immutable source and new output directory required")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	before := fileHash(t, source)
	u := url.URL{Scheme: "file", Path: source}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1&_pragma=query_only(1)&_pragma=cache_size(-8192)&_pragma=mmap_size(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	header, err := index.ReadGenerationHeader(context.Background(), db, "synthetic-v1")
	if err != nil || header.Entries != 10000000 || header.SchemaVersion != index.GenerationSchema {
		t.Fatal("sealed current 10M required", err)
	}
	root, err := index.FindNode(context.Background(), db, "volume", []byte("."))
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{2000, 10000} {
		response, err := query.Hierarchy(context.Background(), db, header, query.Request{SnapshotID: header.SnapshotID, RootID: root.Node.ID, Metric: "logical", Depth: 8, ElementBudget: budget})
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(response)
		if err != nil || len(payload) > query.MaxResponseBytes || len(response.Nodes) > budget {
			t.Fatal("bounded query failed", err)
		}
		if err = os.WriteFile(filepath.Join(destination, fmt.Sprintf("hierarchy-%d.json", budget)), payload, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("budget=%d nodes=%d bytes=%d", budget, len(response.Nodes), len(payload))
	}
	if after := fileHash(t, source); after != before {
		t.Fatal("source changed")
	}
	// The receipt names the immutable input, without adding source paths to the browser DTO.
	receipt, err := json.Marshal(map[string]any{"sourceSHA256": before, "entries": header.Entries, "schema": header.SchemaVersion, "sourceUnchanged": true})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(destination, "receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
}
