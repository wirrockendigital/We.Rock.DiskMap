package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestBoundaryExamplesBudget verifies deterministic clipping without exposing private path bytes.
func TestBoundaryExamplesBudget(t *testing.T) {
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := index.InitializeGeneration(context.Background(), db, "s"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		raw := []byte(fmt.Sprintf("%03d/", i) + strings.Repeat("<", 1000) + "\xff")
		if _, err := db.Exec(`INSERT INTO scan_boundaries VALUES(?,?,?)`, "volume", raw, "SUBVOLUME_CLASSIFICATION_UNAVAILABLE"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := boundaryExamples(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil || len(raw) > 8192 || len(got) == 0 || len(got) > 32 {
		t.Fatal(len(raw), len(got), err)
	}
	for _, b := range got {
		if b.NodeID != nil || !strings.HasSuffix(b.RelativePath, "…") {
			t.Fatal(b)
		}
	}
}
