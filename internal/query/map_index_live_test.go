package query

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/mapindex"
)

// Explicit immutable copies exercise real captured topology without opening a source directory.
func TestMapIndexStoredFixture(t *testing.T) {
	path := os.Getenv("DISKMAP_MAP_FIXTURE")
	if path == "" {
		t.Skip("explicit immutable fixture required")
	}
	snapshot, output := os.Getenv("DISKMAP_MAP_SNAPSHOT"), os.Getenv("DISKMAP_MAP_OUTPUT")
	if snapshot == "" || output == "" {
		t.Fatal("fixture identity/output required")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	h, err := index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	if err = BuildMapIndex(ctx, db, h, output); err != nil {
		t.Fatal(err)
	}
	r, err := mapindex.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root, ids, err := MapIndexIdentities(ctx, db, h, "nas", r)
	if err != nil {
		t.Fatal(err)
	}
	if name := os.Getenv("DISKMAP_MAP_ROOT_NAME"); name != "" {
		found := false
		for _, n := range ids {
			if n.Name == name && n.Kind == "share" {
				root = n.ID
				found = true
				break
			}
		}
		if !found {
			t.Fatal("requested fixture share missing")
		}
	}
	slot, err := r.Slot(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	node, err := r.Record(slot)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, mapindex.RecordBytes)
	node.Encode(raw)
	m := map[string]any{"version": 1, "status": "ready", "snapshotId": snapshot, "indexId": "fixture", "rootId": root, "root": slot, "count": r.Count, "blockBytes": mapindex.BlockBytes, "record": base64.StdEncoding.EncodeToString(raw), "identities": ids}
	encoded, _ := json.Marshal(m)
	if err = os.WriteFile(filepath.Join(output, "manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	total, err := node.Total()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("build_ms=%d records=%d files=%d unknown=%d bytes=%s", time.Since(start).Milliseconds(), r.Count, node.Files, node.Unknown, total.String())
}
