package query

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestSearchMillion qualifies native production projections before authorizing a larger synthetic rebuild.
func TestSearchMillion(t *testing.T) {
	if os.Getenv("DISKMAP_SEARCH_MILLION") != "1" {
		t.Skip("explicit local SSD scale qualification")
	}
	base := os.Getenv("DISKMAP_SEARCH_SCRATCH")
	if base == "" {
		t.Fatal("local SSD scratch required")
	}
	started := time.Now()
	db, h, _ := fixture(t, "wide", 1000000, base)
	build := time.Since(started)
	var seq int
	var name, path string
	if e := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	r := baseSearch(h)
	times := []float64{}
	for _, sortBy := range []string{"name", "size", "type"} {
		for _, term := range []string{"n", "n999", "missing-not-present"} {
			r.Sort = sortBy
			r.Text = term
			for n := 0; n < 5; n++ {
				at := time.Now()
				out, e := Search(context.Background(), db, h, r, "")
				elapsed := time.Since(at)
				if e != nil {
					t.Fatal(sortBy, term, e)
				}
				if term != "missing-not-present" && len(out.Items) != 50 {
					t.Fatal("first fifty absent", sortBy, term, len(out.Items), out.WorkLimited)
				}
				if term == "missing-not-present" && (len(out.Items) != 0 || out.HasMore) {
					t.Fatal("invented match")
				}
				raw, _ := json.Marshal(out)
				if len(raw) > MaxResponseBytes {
					t.Fatal("response budget")
				}
				times = append(times, float64(elapsed.Microseconds())/1000)
				t.Logf("sort=%s text=%q query=%s results=%d workLimited=%t", sortBy, term, elapsed, len(out.Items), out.WorkLimited)
			}
		}
	}
	var rss syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &rss)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	t.Logf("production_nodes=1000000 build_and_seal=%s db_bytes=%d peak_rss_raw=%d heap_inuse=%d goos=%s samples=%v", build, info.Size(), rss.Maxrss, memory.HeapInuse, runtime.GOOS, times)
}
