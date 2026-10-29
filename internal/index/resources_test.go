package index

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
)

// Inspect SQLite's effective limits rather than merely comparing the preset table to itself.
func TestScanCacheBudgets(t *testing.T) {
	for _, mode := range []string{"gentle", "balanced", "fast"} {
		t.Run(mode, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "index.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			policy, err := resources.Resolve(mode, contracts.ResourceLimits{Workers: 1})
			if err != nil {
				t.Fatal(err)
			}
			if err = ConfigureScan(context.Background(), db, policy); err != nil {
				t.Fatal(err)
			}
			for pragma, want := range map[string]int{"cache_size": -policy.SQLiteCacheKiB, "temp.cache_size": -policy.TempCacheKiB, "temp_store": 1, "mmap_size": 0} {
				var got int
				if err = db.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil || got != want {
					t.Fatal(pragma, got, want, err)
				}
			}
			policy.SQLiteCacheKiB++
			if ConfigureScan(context.Background(), db, policy) == nil {
				t.Fatal("unqualified cache override accepted")
			}
		})
	}
}
