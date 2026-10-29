package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestWaitingSQLHonorsCallerDeadline proves a blocked database checkout cannot produce an empty success.
func TestWaitingSQLHonorsCallerDeadline(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	db.SetMaxOpenConns(1)
	connection, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Hierarchy(ctx, db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 10})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("blocked query did not fail within caller deadline", err)
	}
}

// TestChildBufferByteBudget forces large admissible rows and proves the omitted row starts the next page.
func TestChildBufferByteBudget(t *testing.T) {
	db, err := index.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = initializeQueryGeneration(ctx, db, "snapshot"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	parent := "1"
	root := contracts.Node{SnapshotID: "snapshot", ID: parent, OriginalName: ".", VolumeID: "volume", Kind: "directory"}
	rawRoot, _ := json.Marshal(root)
	if _, err = insertQueryFixtureNode(tx, parent, nil, string(rawRoot), "volume", []byte("."), []byte("."), "root"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		n := contracts.Node{SnapshotID: "snapshot", ID: fmt.Sprint(i + 2), VolumeID: "volume", ParentID: &parent, OriginalName: fmt.Sprint(i) + strings.Repeat("x", 50000), Kind: "file"}
		raw, _ := json.Marshal(n)
		if _, err = insertQueryFixtureNode(tx, n.ID, parent, string(raw), "volume", []byte(n.OriginalName), []byte("name"), "synthetic"); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	e := engine{ctx: ctx, db: db, h: index.GenerationHeader{SnapshotID: "snapshot", FileTypeMapping: index.DefaultFileTypeMapping()}, r: Request{SnapshotID: "snapshot", Metric: "logical"}}
	after := int64(0)
	countRows := 0
	pages := 0
	for {
		items, more, next, err := e.children(parent, after, 10000, pages == 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 0 {
			t.Fatal("stalled page")
		}
		size := 0
		for _, v := range items {
			raw, _ := json.Marshal(v.node)
			size += len(raw)
			countRows++
			if v.row != int64(countRows+1) {
				t.Fatal("skipped or repeated child", v.row, countRows)
			}
		}
		if size > MaxResponseBytes {
			t.Fatal("unbounded buffer")
		}
		pages++
		if !more {
			break
		}
		if !e.bufferLimited || next <= after {
			t.Fatal("missing byte continuation")
		}
		after = next
	}
	if countRows != 200 || pages < 2 {
		t.Fatal(countRows, pages)
	}
}

// TestProjectionKindsAndFilter keeps specials out of payload size and preserves exact frozen Unicode mapping.
func TestProjectionKindsAndFilter(t *testing.T) {
	h := index.GenerationHeader{SnapshotID: "snapshot", FileTypeMapping: contracts.FileTypeMapping{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "图", Category: "image"}}}}
	e := engine{h: h, r: Request{SnapshotID: "snapshot", Metric: "allocated", Filter: Filter{}}}
	big := contracts.Bytes("9007199254740993")
	for _, name := range []string{"a.图", "a.unknown", ".图"} {
		n := contracts.Node{ID: "file", SnapshotID: "snapshot", Kind: "file", OriginalName: name, AllocatedBytes: &big}
		v, err := e.decodeNode(n, []byte(name), []byte(name), sql.NullString{}, 1)
		if err != nil {
			t.Fatal(err)
		}
		want := contracts.Bytes("0")
		if name == "a.图" {
			want = big
		}
		matched := Filter{Categories: []string{"image"}}.matches(n, []byte(name), &big, h.FileTypeMapping)
		if matched != (want == big) || v.node.Summary.KnownBytes != big {
			t.Fatal(name, v.node.Summary)
		}
	}
	e.r.Filter.Categories = nil
	for _, kind := range []string{"symlink", "fifo", "socket", "blockDevice", "characterDevice"} {
		n := contracts.Node{ID: "special", SnapshotID: "snapshot", Kind: kind}
		v, err := e.decodeNode(n, nil, nil, sql.NullString{}, 1)
		if err != nil {
			t.Fatal(kind, err)
		}
		if v.node.Summary.SpecialCount != "1" || v.node.Summary.KnownBytes != "0" || v.node.Summary.UnknownFileCount != "0" {
			t.Fatal(v)
		}
	}
}

// TestByteBudgetAndGroupContinuation proves a truncated group starts after exactly the displayed prefix.
func TestByteBudgetAndGroupContinuation(t *testing.T) {
	db, h, _ := fixture(t, "wide", 10000, t.TempDir())
	root := fixtureNodeID(h.SnapshotID, "volume", ".")
	r := Request{SnapshotID: h.SnapshotID, RootID: root, Metric: "logical", Depth: 1, ElementBudget: 10000}
	out, err := hierarchy(context.Background(), db, h, r, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, out)
	raw, _ := json.Marshal(out)
	if len(raw) > MaxResponseBytes {
		t.Fatal("byte budget")
	}
	seen := map[string]bool{}
	var group Node
	for _, n := range out.Nodes {
		seen[n.ID] = true
		if n.Kind == "group" {
			group = n
		}
	}
	if group.GroupReason != "byte-budget" {
		t.Fatal("fixture did not trigger byte cap", len(raw), group.GroupReason)
	}
	r.RootID = group.ListRootID
	r.ElementBudget = 100
	page, err := Children(context.Background(), db, h, r, group.AfterCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Nodes) == 0 {
		t.Fatal("empty continuation")
	}
	for _, n := range page.Nodes {
		if seen[n.ID] {
			t.Fatal("continuation repeats displayed prefix")
		}
	}
}

// TestSourceEnumerationAbsent instruments the package boundary: production query files cannot import source APIs.
func TestSourceEnumerationAbsent(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"io": true, "context": true, "crypto/sha256": true, "database/sql": true, "encoding/base64": true, "encoding/json": true, "errors": true, "fmt": true, "sort": true, "strconv": true, "time": true, "reflect": true, "bytes": true, "math/big": true, "strings": true, "unicode/utf8": true, "github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts": true, "github.com/wirrockendigital/We.Rock.DiskMap/internal/index": true, "github.com/wirrockendigital/We.Rock.DiskMap/internal/security": true}
	// Heap ordering, encoding and lexical path operations do not access the source filesystem.
	for _, name := range []string{"container/heap", "encoding/binary", "encoding/hex", "path", "database/sql/driver", "modernc.org/sqlite"} {
		allowed[name] = true
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, raw, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range f.Imports {
			if !allowed[strings.Trim(i.Path.Value, "\"")] {
				t.Fatal("source-capable import", name, i.Path.Value)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			// SQLite access here is limited to registering the pure literal predicate.
			if ok && id.Name == "sqlite" && sel.Sel.Name != "MustRegisterDeterministicScalarFunction" {
				t.Fatal("unexpected SQLite operation", sel.Sel.Name)
			}
			// Only pure identifier functions are permitted; descriptor/source operations remain forbidden.
			if ok && id.Name == "security" && sel.Sel.Name != "NodeIdentity" && sel.Sel.Name != "ValidID" {
				t.Fatal("unexpected source operation", sel.Sel.Name)
			}
			// These readers inspect only the supplied generation DB, including bounded scan-boundary lookups.
			if ok && id.Name == "index" && sel.Sel.Name != "ValidateSourceProvenance" && sel.Sel.Name != "ReadDirectoryAggregate" && sel.Sel.Name != "ReadGenerationHeader" && sel.Sel.Name != "SearchFold" && sel.Sel.Name != "ReadNode" && sel.Sel.Name != "FindNode" && sel.Sel.Name != "Children" && sel.Sel.Name != "RootNodes" && sel.Sel.Name != "NewNodeReader" && sel.Sel.Name != "RecordedExtension" && sel.Sel.Name != "DirectorySizeUnmeasured" {
				t.Fatal("unexpected index operation", sel.Sel.Name)
			}
			return true
		})
	}
}

// TestPendingDirectoryGroupReserve forces many queued parents to retain exact groups near the response cap.
func TestPendingDirectoryGroupReserve(t *testing.T) {
	ctx := context.Background()
	db, err := index.Open(filepath.Join(t.TempDir(), "branches.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const snapshot = "branches"
	if err = initializeQueryGeneration(ctx, db, snapshot); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := prepareQueryFixtureNode(tx)
	if err != nil {
		t.Fatal(err)
	}
	entries := uint64(0)
	insert := func(rel, parent, kind string) {
		n := contracts.Node{SnapshotID: snapshot, ID: fixtureNodeID(snapshot, "volume", rel), VolumeID: "volume", OriginalName: rel, RelativePath: rel, Kind: kind}
		if i := strings.LastIndexByte(rel, '/'); i >= 0 {
			n.OriginalName = rel[i+1:]
		}
		if parent != "" {
			id := fixtureNodeID(snapshot, "volume", parent)
			n.ParentID = &id
		}
		if kind == "file" {
			b := contracts.Bytes("1")
			n.LogicalBytes = &b
			n.AllocatedBytes = &b
		}
		raw, _ := json.Marshal(n)
		if _, err := stmt.Exec(n.ID, n.ParentID, string(raw), "volume", []byte(n.OriginalName), []byte(rel), "synthetic-metadata"); err != nil {
			t.Fatal(err)
		}
		entries++
	}
	insert(".", "", "directory")
	for d := 0; d < 512; d++ {
		dir := fmt.Sprintf("d%04d", d)
		insert(dir, ".", "directory")
		for f := 0; f < 3; f++ {
			insert(dir+"/"+fmt.Sprintf("f%04d-", f)+strings.Repeat("x", 3000), dir, "file")
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); err != nil {
		t.Fatal(err)
	}
	mapping := index.DefaultFileTypeMapping()
	if err = index.BuildDerivedProjections(ctx, db, mapping); err != nil {
		t.Fatal(err)
	}
	h := index.GenerationHeader{AggregateVersion: index.AggregateVersion, FileTypeMapping: mapping, SnapshotID: snapshot, JobID: "job", Profile: contracts.Profile{ID: "profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: entries, LogicalBytes: "1536"}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	if err = index.FinalizeGeneration(ctx, db, h); err != nil {
		t.Fatal(err)
	}
	h, err = index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	out, err := hierarchy(ctx, db, h, Request{SnapshotID: snapshot, RootID: fixtureNodeID(snapshot, "volume", "."), Metric: "logical", Depth: 8, ElementBudget: 10000}, func() time.Time { return time.Unix(0, 0) })
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, out)
	raw, _ := json.Marshal(out)
	groups := 0
	for _, n := range out.Nodes {
		if n.Kind == "group" {
			groups++
		}
	}
	if len(raw) > MaxResponseBytes || groups < 100 || !out.Truncated || out.Nodes[0].Summary.KnownBytes != "1536" {
		t.Fatal("missing bounded conserved branches", len(raw), groups, out.Truncated)
	}
	t.Logf("nodes=%d groups=%d bytes=%d cap=%d", len(out.Nodes), groups, len(raw), MaxResponseBytes)
}

// TestSoftTimeBudgetConservesAndContinues checks exact residuals and row-boundary progress without sleeping.
func TestSoftTimeBudgetConservesAndContinues(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	base := time.Unix(0, 0)
	calls := 0
	clock := func() time.Time {
		calls++
		if calls >= 150 {
			return base.Add(ExpansionBudget)
		}
		return base
	}
	r := Request{SnapshotID: h.SnapshotID, RootID: fixtureNodeID(h.SnapshotID, "volume", "."), Metric: "logical", Depth: 8, ElementBudget: 1000}
	out, err := hierarchy(context.Background(), db, h, r, clock)
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, out)
	var group Node
	seen := map[string]bool{}
	for _, n := range out.Nodes {
		if n.Kind == "group" {
			group = n
		} else {
			seen[n.ID] = true
		}
	}
	if group.GroupReason != "time-budget" || len(out.Nodes) < 3 || len(out.Nodes) >= 1000 {
		t.Fatal("soft transition absent", len(out.Nodes), group.GroupReason, calls)
	}
	r.RootID = group.ListRootID
	page, err := Children(context.Background(), db, h, r, group.AfterCursor)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range page.Nodes {
		if seen[n.ID] {
			t.Fatal("repeated accepted child")
		}
		seen[n.ID] = true
	}
	if len(seen) != 1000 || page.HasMore {
		t.Fatal("lost time-budget suffix", len(seen), page.HasMore)
	}
	// The reader stops before the next physical row and a new page can resume exactly there.
	calls = 0
	e := engine{ctx: context.Background(), db: db, h: h, r: r, expandUntil: base.Add(ExpansionBudget), now: func() time.Time {
		calls++
		if calls >= 3 {
			return base.Add(ExpansionBudget)
		}
		return base
	}}
	first, more, after, err := e.children(r.RootID, 0, 1000, true)
	if err != nil || !more || !e.timeLimited || len(first) != 3 {
		t.Fatal("reader transition", len(first), more, err)
	}
	e.now = func() time.Time { return base }
	rest, more, _, err := e.children(r.RootID, after, 1000, false)
	if err != nil || more || len(first)+len(rest) != 999 || rest[0].row <= first[len(first)-1].row {
		t.Fatal("reader cursor skipped/repeated", len(rest), more, err)
	}
}
