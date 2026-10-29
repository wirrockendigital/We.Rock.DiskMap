package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"github.com/wirrockendigital/We.Rock.DiskMap/tests/scale"
)

// fixture uses production schema, aggregate construction and sealing with the independent streaming oracle.
func fixture(t *testing.T, shape string, n int, base string, beforeSeal ...func(*sql.DB)) (*sql.DB, index.GenerationHeader, scale.Manifest) {
	t.Helper()
	ctx := context.Background()
	dir, err := os.MkdirTemp(base, "query-index-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	db, err := index.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA cache_size=-8192; PRAGMA temp_store=FILE`); err != nil {
		t.Fatal(err)
	}
	if err = initializeQueryGeneration(ctx, db, "synthetic-v1"); err != nil {
		t.Fatal(err)
	}
	cfg := scale.Config{Count: n, Seed: 7, Shape: shape}
	oracle, err := scale.Oracle(cfg)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { _, err := scale.Generate(writer, cfg); writer.CloseWithError(err); done <- err }()
	defer reader.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := prepareQueryFixtureNode(tx)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(reader)
	for i := 0; i < n; i++ {
		var node contracts.Node
		if err = dec.Decode(&node); err != nil {
			t.Fatal(err)
		}
		rel := strings.TrimPrefix(node.RelativePath, "n0/")
		if rel == "n0" {
			rel = "."
		}
		node.RelativePath = rel
		node.OriginalName = path.Base(rel)
		node.VolumeID = "volume"
		node.ID = fixtureNodeID(node.SnapshotID, "volume", rel)
		if rel != "." {
			id := fixtureNodeID(node.SnapshotID, "volume", path.Dir(rel))
			node.ParentID = &id
		}
		if node.Kind == "directory" {
			node.LogicalBytes = nil
		}
		raw, _ := json.Marshal(node)
		if _, err = stmt.Exec(node.ID, node.ParentID, string(raw), "volume", []byte(node.OriginalName), []byte(rel), "synthetic-metadata"); err != nil {
			t.Fatal(err)
		}
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if n >= 1000000 {
		t.Logf("shape=%s nodes=%d load complete", shape, n)
	}
	if _, err = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); err != nil {
		t.Fatal(err)
	}
	// Optional fixture mutation occurs before production aggregation and sealing.
	for _, adjust := range beforeSeal {
		adjust(db)
	}
	mapping := index.DefaultFileTypeMapping()
	if err = index.BuildDerivedProjections(ctx, db, mapping); err != nil {
		t.Fatal(err)
	}
	if n >= 1000000 {
		t.Logf("shape=%s aggregates complete", shape)
	}
	h := index.GenerationHeader{AggregateVersion: index.AggregateVersion, FileTypeMapping: mapping, SnapshotID: "synthetic-v1", JobID: "job", Profile: contracts.Profile{ID: "profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: uint64(n), LogicalBytes: contracts.Bytes(oracle.KnownSum)}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	if err = index.FinalizeGeneration(ctx, db, h); err != nil {
		t.Fatal(err)
	}
	h, err = index.ReadGenerationHeader(ctx, db, h.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return db, h, oracle
}

// conserved verifies every expanded parent independently and never counts a parent and descendants twice.
func conserved(t *testing.T, r Response) {
	t.Helper()
	byID := map[string]Node{}
	children := map[string][]Node{}
	for _, n := range r.Nodes {
		if _, ok := byID[n.ID]; ok {
			t.Fatal("duplicate", n.ID)
		}
		byID[n.ID] = n
		if n.ParentID != nil {
			children[*n.ParentID] = append(children[*n.ParentID], n)
		}
	}
	if len(r.Nodes) > r.ElementBudget {
		t.Fatal("budget")
	}
	for id, list := range children {
		p, ok := byID[id]
		if !ok {
			t.Fatal("missing parent")
		}
		sum := zero()
		for _, n := range list {
			var err error
			sum, err = merge(sum, n.Summary, false)
			if err != nil {
				t.Fatal(err)
			}
			if n.Virtual && n.RealNodeID != nil {
				t.Fatal("virtual path identity")
			}
		}
		expected := p.Summary
		if p.Kind == "directory" {
			expected.DirectoryCount, _ = arithmetic(expected.DirectoryCount, "1", true)
		}
		if sum != expected {
			t.Fatalf("parent %s: %v != %v", id, sum, expected)
		}
	}
}

// TestHierarchyConservation covers deep/wide/skewed topology, exact huge sums, zero and unknown values.
func TestHierarchyConservation(t *testing.T) {
	for _, shape := range []string{"wide", "deep", "skewed"} {
		t.Run(shape, func(t *testing.T) {
			db, h, oracle := fixture(t, shape, 1000, t.TempDir())
			for _, budget := range []int{1, 2, 10, 2000, 10000} {
				for _, metric := range []string{"logical", "allocated"} {
					r := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: metric, Depth: 2, ElementBudget: budget}
					out, err := Hierarchy(context.Background(), db, h, r)
					if err != nil {
						t.Fatal(err)
					}
					conserved(t, out)
					for _, node := range out.Nodes {
						if node.Kind == "volume" && node.VolumeID != "volume" {
							t.Fatal("virtual volume identity missing", node)
						}
					}
					if metric == "logical" && (string(out.Nodes[0].Summary.KnownBytes) != oracle.KnownSum || string(out.Nodes[0].Summary.UnknownFileCount) != fmt.Sprint(oracle.Unknown)) {
						t.Fatal(out.Nodes[0].Summary, oracle)
					}
				}
			}
		})
	}
}

// TestChildrenContinuation proves indexed stable paging, selector-bound cursors and lossless zero/unknown rows.
func TestChildrenContinuation(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	root := fixtureNodeID(h.SnapshotID, "volume", ".")
	r := Request{SnapshotID: h.SnapshotID, RootID: root, Metric: "logical", Depth: 1, ElementBudget: 73}
	ids := map[string]bool{}
	c := ""
	zeroSeen, unknownSeen := false, false
	for {
		p, err := Children(context.Background(), db, h, r, c)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range p.Nodes {
			if ids[n.ID] {
				t.Fatal("duplicate page")
			}
			ids[n.ID] = true
			zeroSeen = zeroSeen || (n.Summary.KnownBytes == "0" && n.Summary.UnknownFileCount == "0")
			unknownSeen = unknownSeen || n.Summary.UnknownFileCount == "1"
		}
		if !p.HasMore {
			break
		}
		if p.NextCursor == c || p.NextCursor == "" {
			t.Fatal("cursor stuck")
		}
		c = p.NextCursor
	}
	if len(ids) != 999 || !zeroSeen || !unknownSeen {
		t.Fatal(len(ids), zeroSeen, unknownSeen)
	}
	bad := r
	bad.Metric = "allocated"
	if _, err := Children(context.Background(), db, h, bad, c); !errors.Is(err, ErrRequest) {
		t.Fatal("foreign cursor", err)
	}
	bad = r
	bad.SnapshotID = "foreign"
	if _, err := Hierarchy(context.Background(), db, h, bad); !errors.Is(err, ErrSnapshot) {
		t.Fatal(err)
	}
	bad = r
	bad.RootID = "foreign"
	if _, err := Hierarchy(context.Background(), db, h, bad); !errors.Is(err, ErrRoot) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Hierarchy(ctx, db, h, r); err == nil {
		t.Fatal("cancel ignored")
	}
	r.Filter.Categories = []string{"image"}
	r.Mode = "hits-only"
	cache := projectionFixture(t, db, h, r.Metric, r.Filter)
	out, err := HierarchyFiltered(context.Background(), db, cache, h, r)
	if err != nil || out.Nodes[0].Summary.FileCount != "0" {
		t.Fatal(out, err)
	}
	var plan strings.Builder
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT rowid FROM nodes INDEXED BY nodes_parent WHERE parent_id=? AND rowid>? ORDER BY rowid LIMIT 10`, root, 0)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a, b, c int
		var s string
		rows.Scan(&a, &b, &c, &s)
		plan.WriteString(s)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "nodes_parent") || strings.Contains(plan.String(), "TEMP B-TREE") {
		t.Fatal(plan.String())
	}
}

// TestQueryTenMillion uses production SQLite and sealing; large scratch files must be local and explicit.
func TestQueryTenMillion(t *testing.T) {
	base := os.Getenv("DISKMAP_QUERY_SCRATCH")
	if os.Getenv("DISKMAP_QUERY_FULL") != "1" {
		t.Skip("explicit local 10M matrix")
	}
	if base == "" {
		t.Fatal("local scratch required")
	}
	for _, shape := range []string{"wide", "deep", "skewed"} {
		t.Run(shape, func(t *testing.T) {
			start := time.Now()
			db, h, oracle := fixture(t, shape, 10000000, base)
			t.Logf("shape=%s prepared=%s", shape, time.Since(start))
			for _, budget := range []int{2000, 10000} {
				start = time.Now()
				out, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Depth: 8, ElementBudget: budget})
				if err != nil {
					t.Fatal(err)
				}
				conserved(t, out)
				if string(out.Nodes[0].Summary.KnownBytes) != oracle.KnownSum || string(out.Nodes[0].Summary.UnknownFileCount) != fmt.Sprint(oracle.Unknown) {
					t.Fatal("oracle mismatch")
				}
				encoded, _ := json.Marshal(out)
				t.Logf("budget=%d nodes=%d bytes=%d query=%s exact=%s unknown=%s", budget, len(out.Nodes), len(encoded), time.Since(start), out.Nodes[0].Summary.KnownBytes, out.Nodes[0].Summary.UnknownFileCount)
				var usage syscall.Rusage
				syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
				var mem runtime.MemStats
				runtime.ReadMemStats(&mem)
				t.Logf("process_peak_rss_raw=%d heap_inuse=%d goos=%s", usage.Maxrss, mem.HeapInuse, runtime.GOOS)
			}
		})
	}
}
