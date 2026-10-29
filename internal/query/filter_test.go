package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// filterFixture has hand-computable payloads, empty folders and timezone boundaries.
func filterFixture(t *testing.T) (*sql.DB, index.GenerationHeader, map[string]string) {
	t.Helper()
	db, e := index.Open(filepath.Join(t.TempDir(), "source.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if e = initializeQueryGeneration(ctx, db, "filter-fixture"); e != nil {
		t.Fatal(e)
	}
	entries := []struct{ path, kind, size, time string }{{".", "directory", "", "2026-01-01T00:00:00Z"}, {"folder", "directory", "", "2026-01-02T00:00:00Z"}, {"folder/a.jpg", "file", "10", "2026-01-01T01:00:00+01:00"}, {"folder/b.txt", "file", "20", "2026-01-02T00:00:00Z"}, {"zero.jpg", "file", "0", "2026-01-01T00:00:00Z"}, {"unknown.jpg", "file", "", ""}, {"huge.bin", "file", "9007199254740993", "2026-02-01T00:00:00Z"}, {"empty", "directory", "", "2026-01-01T00:00:00Z"}, {"folder/link", "symlink", "", ""}}
	ids := map[string]string{}
	for _, v := range entries {
		ids[v.path] = fixtureNodeID("filter-fixture", "volume", v.path)
	}
	for _, v := range entries {
		n := contracts.Node{SnapshotID: "filter-fixture", ID: ids[v.path], Kind: v.kind, OriginalName: path.Base(v.path), RelativePath: v.path, VolumeID: "volume"}
		if v.path != "." {
			p := ids[path.Dir(v.path)]
			n.ParentID = &p
		}
		if v.size != "" {
			b := contracts.Bytes(v.size)
			n.LogicalBytes = &b
			n.AllocatedBytes = &b
		}
		if v.time != "" {
			s := v.time
			n.ModifiedAt = &s
		}
		raw, _ := json.Marshal(n)
		if _, e = insertQueryFixtureNode(db, n.ID, n.ParentID, string(raw), "volume", []byte(n.OriginalName), []byte(v.path), "synthetic-metadata"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); e != nil {
		t.Fatal(e)
	}
	mapping := index.DefaultFileTypeMapping()
	if e = index.BuildDerivedProjections(ctx, db, mapping); e != nil {
		t.Fatal(e)
	}
	h := index.GenerationHeader{AggregateVersion: index.AggregateVersion, FileTypeMapping: mapping, SnapshotID: "filter-fixture", JobID: "job", Profile: contracts.Profile{ID: "profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-01-01T00:00:00Z", EndedAt: "2026-01-01T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: uint64(len(entries)), LogicalBytes: "9007199254741023"}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	if e = index.FinalizeGeneration(ctx, db, h); e != nil {
		t.Fatal(e)
	}
	h, e = index.ReadGenerationHeader(ctx, db, h.SnapshotID)
	if e != nil {
		t.Fatal(e)
	}
	return db, h, ids
}

// projectionFixture drives the real bounded state machine, including one-row batch boundaries.
func projectionFixture(t *testing.T, source *sql.DB, h index.GenerationHeader, metric string, f Filter) *sql.DB {
	t.Helper()
	db, e := index.Open(filepath.Join(t.TempDir(), "filter.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	if e = InitializeFilterProjection(ctx, source, db, h, metric, f); e != nil {
		t.Fatal(e)
	}
	if e = ValidateFilterProjection(ctx, db, h, metric, f); !errors.Is(e, ErrFilterPending) {
		t.Fatal(e)
	}
	for i := 0; i < 10000; i++ {
		p, e := BuildFilterProjectionStep(ctx, source, db, h, metric, f, 7)
		if e != nil {
			t.Fatal(e)
		}
		if p.Complete {
			return db
		}
	}
	t.Fatal("projection did not complete")
	return nil
}

// TestFilterANDUnionAndDenominators checks original/match weights without subtree double counting.
func TestFilterANDUnionAndDenominators(t *testing.T) {
	db, h, ids := filterFixture(t)
	ctx := context.Background()
	min := contracts.Bytes("0")
	max := contracts.Bytes("10")
	f := Filter{Categories: []string{"image"}, ObjectKinds: []string{"file"}, MinSize: &min, MaxSize: &max}
	cache := projectionFixture(t, db, h, "logical", f)
	for _, mode := range []string{"highlight", "hits-only"} {
		r := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100, Filter: f, Mode: mode}
		out, e := HierarchyFiltered(ctx, db, cache, h, r)
		if e != nil {
			t.Fatal(e)
		}
		if out.OriginalSummary.KnownBytes != "9007199254741023" || out.MatchSummary.KnownBytes != "10" || out.MatchSummary.FileCount != "2" || out.MatchSummary.UnknownFileCount != "0" || out.MatchState != "matches" {
			t.Fatal(out)
		}
		if mode == "highlight" && out.Denominator != out.OriginalSummary || mode == "hits-only" && out.Denominator != out.MatchSummary {
			t.Fatal("denominator", out)
		}
		conserved(t, out)
	}
	// Matching a folder selects its complete subtree, including its nonmatching child and special entry.
	f = Filter{ObjectKinds: []string{"directory"}, Text: "folder", Field: "name"}
	cache = projectionFixture(t, db, h, "logical", f)
	r := Request{SnapshotID: h.SnapshotID, RootID: ids["."], Metric: "logical", Depth: 8, ElementBudget: 100, Filter: f, Mode: "hits-only"}
	out, e := HierarchyFiltered(ctx, db, cache, h, r)
	if e != nil {
		t.Fatal(e)
	}
	if out.MatchSummary.KnownBytes != "30" || out.MatchSummary.FileCount != "2" || out.MatchSummary.SpecialCount != "1" {
		t.Fatal(out.MatchSummary)
	}
	conserved(t, out)
	// The same selected subtree is not counted again when both parent and child match the text.
	f.ObjectKinds = nil
	f.Text = "folder"
	f.Field = "path"
	cache = projectionFixture(t, db, h, "logical", f)
	r.Filter = f
	out, e = HierarchyFiltered(ctx, db, cache, h, r)
	if e != nil || out.MatchSummary.KnownBytes != "30" {
		t.Fatal(out, e)
	}
}

// TestFilterNoMatchUnknownAndIdentity distinguishes no result, unknown bytes, zero and an empty directory.
func TestFilterNoMatchUnknownAndIdentity(t *testing.T) {
	db, h, ids := filterFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ text, root, state, known, unknown string }{{"missing", ".", "no-match", "0", "0"}, {"unknown.jpg", ".", "matches", "0", "1"}, {"empty", "empty", "empty", "0", "0"}, {"missing", "empty", "empty", "0", "0"}} {
		f := Filter{Text: tc.text, Field: "name"}
		cache := projectionFixture(t, db, h, "logical", f)
		r := Request{SnapshotID: h.SnapshotID, RootID: ids[tc.root], Metric: "logical", Depth: 8, ElementBudget: 20, Filter: f, Mode: "hits-only"}
		o, e := HierarchyFiltered(ctx, db, cache, h, r)
		if e != nil || o.MatchState != tc.state || o.MatchSummary.KnownBytes != contracts.Bytes(tc.known) || o.MatchSummary.UnknownFileCount != contracts.Bytes(tc.unknown) {
			t.Fatal(tc, o, e)
		}
		r.Filter.Text = "different"
		if _, e = HierarchyFiltered(ctx, db, cache, h, r); !errors.Is(e, ErrFilterProjection) {
			t.Fatal(e)
		}
	}
	f := Filter{ObjectKinds: []string{"file"}}
	cache := projectionFixture(t, db, h, "logical", f)
	r := Request{SnapshotID: h.SnapshotID, RootID: ids["."], Metric: "logical", Depth: 1, ElementBudget: 1, Filter: f, Mode: "hits-only"}
	p, e := ChildrenFiltered(ctx, db, cache, h, r, "")
	if e != nil || p.NextCursor == "" {
		t.Fatal(p, e)
	}
	r.Mode = "highlight"
	if _, e = ChildrenFiltered(ctx, db, cache, h, r, p.NextCursor); !errors.Is(e, ErrRequest) {
		t.Fatal(e)
	}
}

// TestFilterCanonicalAndCachePlans checks validation and bounded postorder without temporary sorting.
func TestFilterCanonicalAndCachePlans(t *testing.T) {
	db, h, _ := filterFixture(t)
	f := Filter{Text: "x"}
	cache := projectionFixture(t, db, h, "logical", f)
	for _, order := range []string{"depth", "rank"} {
		rows, e := cache.Query(`EXPLAIN QUERY PLAN SELECT id FROM filter_nodes WHERE (`+order+`,id)>(?,?) ORDER BY `+order+`,id ASC LIMIT 256`, 4, "")
		if e != nil {
			t.Fatal(e)
		}
		plan := ""
		for rows.Next() {
			var a, b, c int
			var d string
			rows.Scan(&a, &b, &c, &d)
			plan += d
		}
		rows.Close()
		if strings.Contains(plan, "TEMP B-TREE") || !strings.Contains(plan, "SEARCH filter_nodes USING") {
			t.Fatal(plan)
		}
	}
	for _, bad := range []Filter{{ObjectKinds: []string{"fifo"}}, {Categories: []string{"image", "image"}}, {ModifiedFrom: ptrText("no timezone")}, {MinSize: ptrBytes("01")}, {MinSize: ptrBytes("2"), MaxSize: ptrBytes("1")}} {
		if _, e := bad.Canonical(); e == nil {
			t.Fatal(bad)
		}
	}
}
func ptrText(s string) *string                    { return &s }
func ptrBytes(s contracts.Bytes) *contracts.Bytes { return &s }

// TestFilterCancellationRestartAndSearch checks atomic cursors and identical list predicates.
func TestFilterCancellationRestartAndSearch(t *testing.T) {
	source, h, ids := filterFixture(t)
	f := Filter{Categories: []string{"image"}, MaxSize: ptrBytes("10"), ObjectKinds: []string{"file"}}
	cache, e := index.Open(filepath.Join(t.TempDir(), "cache.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer cache.Close()
	ctx := context.Background()
	if e = InitializeFilterProjection(ctx, source, cache, h, "logical", f); e != nil {
		t.Fatal(e)
	}
	first, e := BuildFilterProjectionStep(ctx, source, cache, h, "logical", f, 1)
	if e != nil || first.ProcessedRows != "1" {
		t.Fatal(first, e)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = BuildFilterProjectionStep(cancelled, source, cache, h, "logical", f, 256); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	persisted, e := readFilterState(ctx, cache)
	if e != nil || persisted.processed != "1" {
		t.Fatal(persisted, e)
	}
	for i := 0; i < 100; i++ {
		p, e := BuildFilterProjectionStep(ctx, source, cache, h, "logical", f, 1)
		if e != nil {
			t.Fatal(e)
		}
		if p.Complete {
			break
		}
	}
	if e = ValidateFilterProjection(ctx, cache, h, "logical", f); e != nil {
		t.Fatal(e)
	}
	if e = ValidateFilterProjection(ctx, cache, h, "allocated", f); !errors.Is(e, ErrFilterProjection) {
		t.Fatal(e)
	}
	r := baseSearch(h)
	r.RootID = ids["."]
	r.Filter = f
	r.Limit = 1
	a, e := Search(ctx, source, h, r, "")
	if e != nil || len(a.Items) != 1 || !a.HasMore {
		t.Fatal(a, e)
	}
	b, e := Search(ctx, source, h, r, a.NextCursor)
	if e != nil || len(b.Items) != 1 || b.HasMore || a.Items[0].NodeID == b.Items[0].NodeID {
		t.Fatal(b, e)
	}
	r.Filter.MaxSize = ptrBytes("20")
	if _, e = Search(ctx, source, h, r, a.NextCursor); !errors.Is(e, ErrSearch) {
		t.Fatal(e)
	}
}
