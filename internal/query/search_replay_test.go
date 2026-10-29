package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"net/url"
	"os"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"
)

// TestReplaySearchMillion measures final query code only after the original writer has exited and checkpointed.
func TestReplaySearchMillion(t *testing.T) {
	path := os.Getenv("DISKMAP_SEARCH_REPLAY")
	if path == "" {
		t.Skip("explicit closed local 1M index required")
	}
	u := url.URL{Scheme: "file", Path: path}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&_pragma=query_only(1)&_pragma=cache_size(-8192)")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	h, e := index.ReadGenerationHeader(context.Background(), db, "synthetic-v1")
	if e != nil || h.Entries != 1000000 {
		t.Fatal("sealed million required", e)
	}
	r := baseSearch(h)
	durations := []time.Duration{}
	for _, order := range []string{"name", "size", "type"} {
		for _, term := range []string{"n", "n999", "missing-not-present"} {
			r.Sort = order
			r.Text = term
			for repeat := 0; repeat < 5; repeat++ {
				at := time.Now()
				out, e := Search(context.Background(), db, h, r, "")
				elapsed := time.Since(at)
				if e != nil {
					t.Fatal(order, term, e)
				}
				if term == "missing-not-present" {
					if len(out.Items) != 0 || out.HasMore {
						t.Fatal("false match")
					}
				} else if len(out.Items) != 50 || out.WorkLimited {
					t.Fatal("first fifty incomplete", order, term, len(out.Items), out.WorkLimited)
				}
				raw, _ := json.Marshal(out)
				if len(raw) > MaxResponseBytes {
					t.Fatal("response limit")
				}
				durations = append(durations, elapsed)
				if repeat == 0 && out.HasMore {
					next, e := Search(context.Background(), db, h, r, out.NextCursor)
					if e != nil {
						t.Fatal(e)
					}
					ids := map[string]bool{}
					for _, item := range out.Items {
						ids[item.NodeID] = true
					}
					for _, item := range next.Items {
						if ids[item.NodeID] {
							t.Fatal("overlapping pages")
						}
					}
				}
			}
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[(95*len(durations)+99)/100-1]
	if p95 >= 2*time.Second {
		t.Fatal("first-fifty p95 budget", p95)
	}
	info, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	var rss syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &rss)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("sealed_nodes=%d samples=%d p95=%s max=%s database_bytes=%d peak_rss_raw=%d heap_inuse=%d goos=%s", h.Entries, len(durations), p95, durations[len(durations)-1], info.Size(), rss.Maxrss, m.HeapInuse, runtime.GOOS)
	// dbstat is a read-only physical footprint observation, outside the query-latency samples.
	rows, e := db.Query(`SELECT CASE WHEN name LIKE 'search_%' OR name LIKE 'sqlite_autoindex_search_%' THEN 'search' ELSE 'other' END,sum(pgsize) FROM dbstat GROUP BY 1`)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	for rows.Next() {
		var group string
		var size int64
		if e = rows.Scan(&group, &size); e != nil {
			t.Fatal(e)
		}
		t.Logf("physical_pages=%s bytes=%d", group, size)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
}
