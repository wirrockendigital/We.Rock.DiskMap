package mapindex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This opt-in fixture repacks copied, unsealed artifacts for browser locality measurements.
func TestPackBrowserFixture(t *testing.T) {
	dir := os.Getenv("DISKMAP_PACK_FIXTURE")
	if dir == "" {
		t.Skip("opt-in packing fixture")
	}
	if err := Pack(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	root, err := r.Slot(context.Background(), m["rootId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	record, err := r.Record(root)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, RecordBytes)
	record.Encode(data)
	m["root"] = root
	m["record"] = base64.StdEncoding.EncodeToString(data)
	for _, value := range m["identities"].([]any) {
		identity := value.(map[string]any)
		slot, err := r.Slot(context.Background(), identity["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		identity["slot"] = slot
	}
	raw, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}
