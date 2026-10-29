package indexbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
)

// TestCompactWriteBenchmark is an explicit isolated write diagnostic, never a production scan or seal.
// The caller selects an existing artifact directory; each mode creates a fresh uniquely named child.
func TestCompactWriteBenchmark(t *testing.T) {
	base := os.Getenv("DISKMAP_COMPACT_BENCH_DIR")
	if base == "" {
		t.Skip("explicit compact diagnostic directory required")
	}
	if !filepath.IsAbs(base) || filepath.Clean(base) != base {
		t.Fatal("absolute clean artifact directory required")
	}
	info, err := os.Lstat(base)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("existing artifact directory required", err)
	}
	const files = 50000
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			dir, err := os.MkdirTemp(base, "compact-write-"+strings.ToLower(mode)+"-")
			if err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(dir, "index.db")
			db, err := index.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			policy, err := resources.Resolve("gentle", contracts.ResourceLimits{Workers: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err = index.ConfigureScan(ctx, db, policy); err != nil {
				t.Fatal(err)
			}
			// Both arms retain FULL durability; only rollback-journal versus WAL layout changes.
			var actualMode string
			if err = db.QueryRowContext(ctx, "PRAGMA journal_mode="+mode).Scan(&actualMode); err != nil || !strings.EqualFold(actualMode, mode) {
				t.Fatal("journal mode", actualMode, err)
			}
			if _, err = db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
				t.Fatal(err)
			}
			var syncMode, cache int
			if err = db.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&syncMode); err != nil || syncMode != 2 {
				t.Fatal("FULL required", syncMode, err)
			}
			if err = db.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cache); err != nil || cache != -2048 {
				t.Fatal("gentle cache required", cache, err)
			}
			if err = index.InitializeGeneration(ctx, db, "compact-benchmark"); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			w, err := index.NewNodeWriter(ctx, tx, "compact-benchmark")
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close()
			rid, err := w.AddRoot("volume", "volume", nil)
			if err != nil {
				t.Fatal(err)
			}
			root, err := w.Insert(rid, 0, []byte("."), "directory", nil, nil, "benchmark-root", nil)
			if err != nil {
				t.Fatal(err)
			}
			bytes := contracts.Bytes("4096")
			for i := 0; i < files; i++ {
				name := fmt.Sprintf("%s-%06d.dat", strings.Repeat("segment", 27), i)
				if _, err = w.Insert(rid, root, []byte(name), "file", &bytes, &bytes, fmt.Sprintf("benchmark-object-%d", i), nil); err != nil {
					t.Fatal(err)
				}
			}
			insertTime := time.Since(start)
			start = time.Now()
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			commitTime := time.Since(start)
			sizes := func() map[string]int64 {
				result := map[string]int64{}
				for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
					info, err := os.Stat(dbPath + suffix)
					if err == nil {
						result["index.db"+suffix] = info.Size()
					} else if os.IsNotExist(err) {
						result["index.db"+suffix] = 0
					} else {
						t.Fatal(err)
					}
				}
				return result
			}
			afterCommit := sizes()
			start = time.Now()
			if err = index.BuildDerivedProjectionsWithCache(ctx, db, index.DefaultFileTypeMapping(), policy.TempCacheKiB); err != nil {
				t.Fatal(err)
			}
			aggregateTime := time.Since(start)
			var rows int
			if err = db.QueryRowContext(ctx, "SELECT count(*) FROM nodes").Scan(&rows); err != nil || rows != files+1 {
				t.Fatal("row count", rows, err)
			}
			report := map[string]any{"mode": actualMode, "synchronous": syncMode, "cacheKiB": -cache, "rows": rows, "insertSeconds": insertTime.Seconds(), "commitSeconds": commitTime.Seconds(), "aggregateSeconds": aggregateTime.Seconds(), "afterCommit": afterCommit, "afterAggregate": sizes(), "artifact": dir, "sealed": false}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			report["afterClose"] = sizes()
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("COMPACT_WRITE_BENCH %s\n", encoded)
		})
	}
}
