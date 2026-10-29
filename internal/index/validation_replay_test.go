package index

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestValidationReplay measures the full validator on an explicit disposable sealed copy.
func TestValidationReplay(t *testing.T) {
	file := os.Getenv("DISKMAP_VALIDATION_COPY")
	if file == "" {
		t.Skip("isolated validation replay not requested")
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
	if _, err = db.Exec(`PRAGMA cache_size=-2048; PRAGMA mmap_size=0`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = VerifyGeneration(context.Background(), db, h.SnapshotID, h.Profile.ID, h.JobID); err != nil {
		t.Fatal(err)
	}
	t.Logf("validationSeconds=%.3f entries=%d", time.Since(start).Seconds(), h.Entries)
}
