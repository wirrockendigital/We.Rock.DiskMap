package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestNativeSparseHierarchyStages is an opt-in diagnostic, not a replacement performance gate.
// Each stage is independently timed; their sum is not the production request latency.
func TestNativeSparseHierarchyStages(t *testing.T) {
	path := os.Getenv("DISKMAP_SPARSE_HIERARCHY_STAGE")
	if path == "" {
		t.Skip("explicit closed synthetic skewed index required")
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1&_pragma=query_only(1)&_pragma=cache_size(-8192)&_pragma=temp_store(1)&_pragma=temp.cache_size(-2048)&_pragma=mmap_size(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	h, err := index.ReadGenerationHeader(ctx, db, "synthetic-v1")
	if err != nil || h.Entries != 10000000 {
		t.Fatal("qualified synthetic 10M required", err)
	}
	storedRoot, err := index.FindNode(ctx, db, "volume", []byte("n63"))
	if err != nil {
		t.Fatal(err)
	}
	rootID := storedRoot.Node.ID
	r := Request{SnapshotID: h.SnapshotID, RootID: rootID, Metric: "logical", Depth: 8, ElementBudget: 2000, Filter: Filter{Categories: []string{}}}
	r, err = r.Validate()
	if err != nil {
		t.Fatal(err)
	}
	e := engine{ctx: ctx, db: db, h: h, r: r}
	root, err := e.root()
	if err != nil || root.node.Summary.FileCount != "31250" || root.node.Summary.KnownBytes != "0" {
		t.Fatal("synthetic directory-63 oracle", err)
	}
	timings := map[string][]time.Duration{}
	measure := func(name string, at time.Time) { timings[name] = append(timings[name], time.Since(at)) }
	// Production samples are contiguous: no additional stage reads warm the next request.
	var out Response
	for _, budget := range []int{2000, 10000} {
		request := r
		request.ElementBudget = budget
		for sample := 0; sample < 20; sample++ {
			at := time.Now()
			result, err := Hierarchy(ctx, db, h, request)
			measure(fmt.Sprintf("production-total-%d", budget), at)
			if err != nil {
				t.Fatal("production hierarchy", budget, sample, err)
			}
			if len(result.Nodes) > budget {
				t.Fatal("node budget")
			}
			if budget == 2000 {
				out = result
			}
			t.Logf("budget=%d depth=8 sample=%d production_nodes=%d truncated=%t", budget, sample, len(result.Nodes), result.Truncated)
		}
	}
	// These separate 2000-budget component probes deliberately run only after both production series.
	for sample := 0; sample < 20; sample++ {
		stageCtx, stop := context.WithTimeout(ctx, QueryTimeout)
		at := time.Now()
		raw, err := index.Children(stageCtx, db, rootID, 0, 1999)

		measure("native-tree-reader", at)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) != 1999 {
			t.Fatal("unexpected bounded row count", len(raw))
		}
		// The last SQL row is the production lookahead and is never decoded.
		raw = raw[:1998]
		// The native decoder may read directory aggregates; label this separately from tree reconstruction.
		stageCtx, stop = context.WithTimeout(ctx, QueryTimeout)
		e.ctx = stageCtx
		at = time.Now()
		decoded := make([]record, 0, len(raw))
		bufferBytes := 0
		for _, v := range raw {
			if err = stageCtx.Err(); err != nil {
				stop()
				t.Fatal(err)
			}
			n, er := e.decodeRecord(v)
			if er != nil {
				stop()
				t.Fatal(er)
			}
			b, er := json.Marshal(n.node)
			if er != nil {
				stop()
				t.Fatal(er)
			}
			bufferBytes += len(v.Path) + len(v.Name) + len(b)
			decoded = append(decoded, n)
		}
		measure("decode-aggregate-buffer-marshal", at)
		stop()
		// Repeat the production's three exact summary subtractions and accepted-node serialization.
		at = time.Now()
		total, original, matched := root.node.Summary, root.node.OriginalSummary, root.node.MatchSummary
		encodedBytes := 0
		for _, v := range decoded {
			for _, s := range []*Summary{&total, &original, &matched} {
				*s, err = merge(*s, v.node.Summary, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			b, er := json.Marshal(v.node)
			if er != nil {
				t.Fatal(er)
			}
			encodedBytes += len(b)
		}
		measure("summary-accepted-marshal", at)
		at = time.Now()
		b, err := json.Marshal(out)
		measure("final-response-marshal", at)
		if err != nil || len(b) > MaxResponseBytes {
			t.Fatal("response bound", err)
		}
		if sample == 0 {
			t.Logf("diagnostic_rows=%d retained_cost=%d accepted_node_bytes=%d actual_response_bytes=%d", len(raw), bufferBytes, encodedBytes, len(b))
		}
	}
	for _, name := range []string{"production-total-2000", "production-total-10000", "native-tree-reader", "decode-aggregate-buffer-marshal", "summary-accepted-marshal", "final-response-marshal"} {
		v := timings[name]
		sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
		t.Logf("stage=%s samples=%d p95=%s max=%s", name, len(v), v[18], v[19])
	}
	var usage syscall.Rusage
	if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		t.Fatal(err)
	}
	rss := usage.Maxrss
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	t.Logf("peak_rss_bytes=%d goos=%s runtime=%s cache=8MiB temp_cache=2MiB mmap=0 sequential_diagnostic_no_cold_claim", rss, runtime.GOOS, runtime.Version())
}
