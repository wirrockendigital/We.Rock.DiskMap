package query

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestFilterMillionReplay measures the cache over an existing sealed production source, never regenerating it.
func TestFilterMillionReplay(t *testing.T) {
	sourcePath := os.Getenv("DISKMAP_FILTER_SOURCE")
	scratch := os.Getenv("DISKMAP_FILTER_SCRATCH")
	if sourcePath == "" || scratch == "" {
		t.Skip("explicit existing local source and scratch required")
	}
	u := url.URL{Scheme: "file", Path: sourcePath}
	source, e := sql.Open("sqlite", u.String()+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()
	source.SetMaxOpenConns(1)
	var snapshot string
	if e = source.QueryRow(`SELECT snapshot_id FROM generation_identity`).Scan(&snapshot); e != nil {
		t.Fatal(e)
	}
	h, e := index.ReadGenerationHeader(context.Background(), source, snapshot)
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(scratch, "filter-projection-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cache, e := index.Open(filepath.Join(dir, "cache.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer cache.Close()
	if _, e = cache.Exec(`PRAGMA journal_mode=MEMORY;PRAGMA synchronous=OFF;PRAGMA cache_size=-2048`); e != nil {
		t.Fatal(e)
	}
	f := Filter{ObjectKinds: []string{"file"}, MinSize: ptrBytes("0")}
	ctx := context.Background()
	started := time.Now()
	if e = InitializeFilterProjection(ctx, source, cache, h, "logical", f); e != nil {
		t.Fatal(e)
	}
	steps := 0
	last := ""
	var longest time.Duration
	for {
		before := time.Now()
		p, e := BuildFilterProjectionStep(ctx, source, cache, h, "logical", f, 256)
		if e != nil {
			t.Fatal(e)
		}
		elapsed := time.Since(before)
		if elapsed > longest {
			longest = elapsed
		}
		steps++
		if p.Phase != last {
			t.Logf("phase=%s elapsed=%s processed=%s", p.Phase, time.Since(started), p.ProcessedRows)
			last = p.Phase
		}
		if p.Complete {
			break
		}
	}
	build := time.Since(started)
	before := time.Now()
	out, e := HierarchyFiltered(ctx, source, cache, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 4, ElementBudget: 2000, Filter: f, Mode: "hits-only"})
	if e != nil {
		t.Fatal(e)
	}
	conserved(t, out)
	if out.MatchSummary.KnownBytes != h.LogicalBytes || out.MatchSummary.UnknownFileCount != "0" {
		t.Fatal(out.MatchSummary, h.LogicalBytes)
	}
	queryTime := time.Since(before)
	st, e := os.Stat(filepath.Join(dir, "cache.db"))
	if e != nil {
		t.Fatal(e)
	}
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("source_nodes=%d steps=%d cache_build=%s max_step=%s query=%s cache_bytes=%d peak_rss_raw=%d heap_inuse=%d known=%s", h.Entries, steps, build, longest, queryTime, st.Size(), usage.Maxrss, mem.HeapInuse, out.MatchSummary.KnownBytes)
}
