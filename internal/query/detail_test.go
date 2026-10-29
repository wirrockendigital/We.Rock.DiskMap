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
	"testing"
)

// detailFixture has a hand-computed oracle, not a second implementation of aggregation.
func detailFixture(t *testing.T, unqualified ...bool) (*sql.DB, index.GenerationHeader, map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, e := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = initializeQueryGeneration(ctx, db, "detail-snapshot"); e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	bytes := func(v string) *contracts.Bytes { x := contracts.Bytes(v); return &x }
	for _, v := range []struct {
		p, k  string
		l, a  *contracts.Bytes
		links int
	}{{".", "directory", nil, nil, 0}, {"empty", "directory", nil, nil, 0}, {"d", "directory", nil, nil, 0}, {"a.txt", "file", bytes("9007199254740993"), bytes("4096"), 1}, {"zero.bin", "file", bytes("0"), bytes("0"), 1}, {"d/h1.bin", "file", bytes("10"), bytes("512"), 2}, {"d/h2.bin", "file", bytes("10"), bytes("512"), 2}, {"d/unknown.txt", "file", nil, nil, 1}, {"sym", "symlink", nil, nil, 1}} {
		id := fixtureNodeID("detail-snapshot", "volume", v.p)
		ids[v.p] = id
		n := contracts.Node{SnapshotID: "detail-snapshot", ID: id, OriginalName: path.Base(v.p), Kind: v.k, VolumeID: "volume", RelativePath: v.p, LogicalBytes: v.l, AllocatedBytes: v.a}
		if v.p != "." {
			parent := fixtureNodeID("detail-snapshot", "volume", path.Dir(v.p))
			n.ParentID = &parent
		}
		if v.links > 0 {
			links := v.links
			n.LinkCount = &links
		}
		raw, e := json.Marshal(n)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = insertQueryFixtureNode(db, id, n.ParentID, string(raw), "volume", []byte(n.OriginalName), []byte(v.p), "fixture"); e != nil {
			t.Fatal(e)
		}
	}
	db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`)
	mapping := index.DefaultFileTypeMapping()
	if e = index.BuildDerivedProjections(ctx, db, mapping); e != nil {
		t.Fatal(e)
	}
	h := index.GenerationHeader{SnapshotID: "detail-snapshot", JobID: "job", Profile: contracts.Profile{ID: "p", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat", AllocatedQualification}, State: "complete", Entries: 9, LogicalBytes: "9007199254741013", UnknownLogicalEntries: 1, FileTypeMapping: mapping, AggregateVersion: index.AggregateVersion}
	if len(unqualified) > 0 && unqualified[0] {
		h.Methods = []string{"descriptor-no-follow-stat"}
	}
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

// Direct files are retained, hardlinks count per path, and unknowns never become zero.
func TestDetailHandOracle(t *testing.T) {
	db, h, ids := detailFixture(t)
	r := DetailRequest{SnapshotID: h.SnapshotID, RootID: "nas", NodeID: ids["."], Metric: "logical", Operation: "details"}
	out, e := Detail(context.Background(), db, h, r)
	if e != nil {
		t.Fatal(e)
	}
	a := out.Aggregate
	if a.Direct.FileCount != "2" || a.Direct.DirectoryCount != "2" || a.Subtree.FileCount != "5" || a.Subtree.Logical.KnownBytes != "9007199254741013" || a.Subtree.Logical.UnknownFileCount != "1" || a.Subtree.Allocated.KnownBytes != "5120" || a.Subtree.SpecialCount != "1" || a.Subtree.Types["document"].FileCount != "2" {
		t.Fatal(a)
	}
	r.NodeID = ids["a.txt"]
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || out.Node.OwnerID != nil || out.GroupID != nil || *out.Node.LogicalBytes != "9007199254740993" || !out.Allocated.Selectable {
		t.Fatal(out, e)
	}
	r.NodeID = ids["zero.bin"]
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || out.Node.LogicalBytes == nil || *out.Node.LogicalBytes != "0" {
		t.Fatal(out, e)
	}
	r.NodeID = ids["d/unknown.txt"]
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || out.Node.LogicalBytes != nil || out.Numerator.UnknownFileCount != "1" {
		t.Fatal(out, e)
	}
	r.NodeID = ids["sym"]
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || out.Node.LogicalBytes != nil || out.Numerator.SpecialCount != "1" {
		t.Fatal(out, e)
	}
	r.NodeID = "nas"
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || !out.Virtual || out.Node != nil || out.Aggregate.Direct != nil || out.Aggregate.Subtree.DirectoryCount != "3" || out.Aggregate.Subtree.Logical.KnownBytes != "9007199254741013" {
		t.Fatal(out, e)
	}
}

// Ranking pages remain deterministic through equal sizes and distinguish direct directories from files.
func TestDetailRankingContinuationAndScope(t *testing.T) {
	db, h, ids := detailFixture(t)
	r := DetailRequest{SnapshotID: h.SnapshotID, RootID: "nas", NodeID: ids["."], Metric: "logical", Operation: "recursive-files", Limit: 1}
	seen := map[string]bool{}
	for n := 0; n < 8; n++ {
		out, e := Detail(context.Background(), db, h, r)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range out.Ranking.Items {
			if v.Kind != "file" || seen[v.NodeID] {
				t.Fatal(v)
			}
			seen[v.NodeID] = true
		}
		if !out.Ranking.HasMore {
			break
		}
		if out.Ranking.NextCursor == "" {
			t.Fatal("missing continuation")
		}
		r.Cursor = out.Ranking.NextCursor
	}
	if len(seen) != 5 {
		t.Fatal(seen)
	}
	r.Operation = "direct-folders"
	r.Cursor = ""
	r.Limit = 20
	out, e := Detail(context.Background(), db, h, r)
	if e != nil || len(out.Ranking.Items) != 2 {
		t.Fatal(out, e)
	}
	for _, v := range out.Ranking.Items {
		if v.Kind != "directory" || v.ParentID == nil || *v.ParentID != ids["."] {
			t.Fatal(v)
		}
	}
	r.Operation = "details"
	r.Limit = 0
	r.RootID = ids["empty"]
	r.NodeID = ids["a.txt"]
	if _, e = Detail(context.Background(), db, h, r); !errors.Is(e, ErrRoot) {
		t.Fatal(e)
	}
	r.RootID = "nas"
	r.NodeID = "q-group-no-persistent-node"
	if _, e = Detail(context.Background(), db, h, r); !errors.Is(e, ErrDetailUnavailable) {
		t.Fatal(e)
	}
}

// Deadlines and missing method qualification never become empty-success or substituted allocated bytes.
func TestDetailDeadlineAndMetric(t *testing.T) {
	db, h, ids := detailFixture(t, true)
	r := DetailRequest{SnapshotID: h.SnapshotID, RootID: "nas", NodeID: ids["."], Metric: "allocated", Operation: "details"}
	if _, e := Detail(context.Background(), db, h, r); !errors.Is(e, ErrDetailMetric) {
		t.Fatal(e)
	}
	r.Metric = "logical"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Detail(ctx, db, h, r); e == nil {
		t.Fatal("cancelled query succeeded")
	}
	r.Operation = "recursive-files"
	r.Limit = 21
	if r.Validate() == nil {
		t.Fatal("unbounded ranking")
	}
}

// A complete native scan proves an empty ranking; a real result cursor still binds the view root.
func TestDetailRankingCompleteNativeScan(t *testing.T) {
	db, h, _ := fixture(t, "wide", 5000, t.TempDir())
	r := DetailRequest{SnapshotID: h.SnapshotID, RootID: "nas", NodeID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Operation: "direct-folders", Limit: 20}
	out, e := Detail(context.Background(), db, h, r)
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Ranking.Items) != 0 || out.Ranking.WorkLimited || out.Ranking.HasMore || out.Ranking.NextCursor != "" {
		t.Fatal(out.Ranking)
	}
	r.Operation = "recursive-files"
	out, e = Detail(context.Background(), db, h, r)
	if e != nil || !out.Ranking.HasMore || out.Ranking.NextCursor == "" {
		t.Fatal(out, e)
	}
	r.Cursor = out.Ranking.NextCursor
	r.RootID = r.NodeID
	if _, e = Detail(context.Background(), db, h, r); !errors.Is(e, ErrRequest) {
		t.Fatal("cursor allowed a changed view root", e)
	}
}

// Valid node IDs from another request snapshot cannot cross the pure query boundary.
func TestDetailRejectsForeignRequestSnapshot(t *testing.T) {
	db, h, ids := detailFixture(t)
	r := DetailRequest{SnapshotID: "foreign", RootID: "nas", NodeID: ids["a.txt"], Metric: "logical", Operation: "details"}
	if _, e := Detail(context.Background(), db, h, r); !errors.Is(e, ErrSnapshot) {
		t.Fatal(e)
	}
}
