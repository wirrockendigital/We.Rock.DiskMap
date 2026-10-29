package mapindex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// An opt-in fixture exports the production binary format for real browser qualification.
func TestBrowserFixture(t *testing.T) {
	dir := os.Getenv("DISKMAP_BROWSER_FIXTURE")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	count, err := strconv.Atoi(os.Getenv("DISKMAP_BROWSER_FILES"))
	if err != nil || count < 1 {
		t.Fatal("invalid file count")
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Add(Input{ID: "1", Source: 1, Kind: Directory}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err = b.Add(Input{ID: strconv.Itoa(i + 2), Source: uint64(i + 2), Parent: "1", Kind: File, Bytes: [4]Size{{Lo: uint64(1 + i%1000)}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = b.Finish(); err != nil {
		t.Fatal(err)
	}
	if err = SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	n, err := r.Record(1)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, RecordBytes)
	n.Encode(raw)
	manifest := map[string]any{"version": 1, "status": "ready", "snapshotId": "fixture", "indexId": "fixture", "rootId": "1", "root": 1, "count": r.Count, "blockBytes": BlockBytes, "record": base64.StdEncoding.EncodeToString(raw), "identities": []map[string]any{{"slot": 1, "id": "1", "name": "Fixture", "kind": "other"}}}
	encoded, _ := json.Marshal(manifest)
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
