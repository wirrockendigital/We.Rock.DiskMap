package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/tests/scale"
)

// TestReplayTenMillion repeats only the final query binary against fully closed retained synthetic indexes.
// The caller must wait until each generator/aggregator/sealer process has exited before invoking this test.
func TestReplayTenMillion(t *testing.T) {
	base := os.Getenv("DISKMAP_QUERY_REPLAY")
	if base == "" {
		t.Skip("explicit retained local index required")
	}
	for _, shape := range []string{"wide", "deep", "skewed"} {
		t.Run(shape, func(t *testing.T) {
			u := url.URL{Scheme: "file", Path: filepath.Join(base, shape+".db")}
			db, err := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=query_only(1)&_pragma=cache_size(-8192)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			h, err := index.ReadGenerationHeader(context.Background(), db, "synthetic-v1")
			if err != nil || h.Entries != 10000000 {
				t.Fatal("not a sealed 10M index", err, h.Entries)
			}
			oracle, err := scale.Oracle(scale.Config{Count: 10000000, Seed: 7, Shape: shape})
			if err != nil {
				t.Fatal(err)
			}
			root, err := index.FindNode(context.Background(), db, "volume", []byte("."))
			if err != nil {
				t.Fatal(err)
			}
			for _, budget := range []int{2000, 10000} {
				start := time.Now()
				out, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: root.Node.ID, Metric: "logical", Depth: 8, ElementBudget: budget})
				if err != nil {
					t.Fatal(err)
				}
				conserved(t, out)
				if string(out.Nodes[0].Summary.KnownBytes) != oracle.KnownSum || string(out.Nodes[0].Summary.UnknownFileCount) != fmt.Sprint(oracle.Unknown) {
					t.Fatal("oracle mismatch")
				}
				encoded, _ := json.Marshal(out)
				var usage syscall.Rusage
				syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				t.Logf("shape=%s budget=%d nodes=%d bytes=%d query=%s exact=%s unknown=%s peak_rss_raw=%d heap_inuse=%d goos=%s", shape, budget, len(out.Nodes), len(encoded), time.Since(start), out.Nodes[0].Summary.KnownBytes, out.Nodes[0].Summary.UnknownFileCount, usage.Maxrss, mem.HeapInuse, runtime.GOOS)
			}
		})
	}
}
