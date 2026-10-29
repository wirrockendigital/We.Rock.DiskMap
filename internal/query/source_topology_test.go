package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"math/big"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// sourceTopologyFixture authors native catalog authority in a local sealed fixture; it makes no NAS claim.
func sourceTopologyFixture(t *testing.T, transforms ...func(*sql.DB, *index.GenerationHeader)) (*sql.DB, index.GenerationHeader, map[string]string) {
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
		if _, e = insertQueryFixtureNode(db, id, n.ParentID, string(raw), "volume", []byte(n.OriginalName), []byte(v.p), "1:256:0:0"); e != nil {
			t.Fatal(e)
		}
	}
	db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`)
	mapping := index.DefaultFileTypeMapping()
	h := index.GenerationHeader{SnapshotID: "detail-snapshot", JobID: "job", Profile: contracts.Profile{ID: "p", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat", AllocatedQualification}, State: "complete", Entries: 9, LogicalBytes: "9007199254741013", UnknownLogicalEntries: 1, FileTypeMapping: mapping, AggregateVersion: index.AggregateVersion}

	h.Manifest.CoveredTargetIDs = []string{"volume", "share-d", "share-empty"}
	h.Manifest.Roots[0].PhysicalPath = "/fixture"
	h.Manifest.Roots[0].Binding = &scopemanifest.RootIdentity{Path: "/fixture", Device: "1", Inode: "256", MountID: "1"}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, false)
	h.Manifest.Provenance.Authority = "native-inventory"
	h.Manifest.Provenance.Roots[0].VolumeName = "Historical Volume"
	for _, name := range []string{"d", "empty"} {
		id := "share-" + name
		h.Manifest.Provenance.Roots[0].Areas = append(h.Manifest.Provenance.Roots[0].Areas, scopemanifest.SourceArea{SourceID: &id, VolumeID: "volume", Name: "Historical " + name, RelativePath: []byte(name), State: "reachable", Qualification: "qualified", Binding: &scopemanifest.RootIdentity{Path: "/fixture/" + name, Device: "1", Inode: "256", MountID: "1"}})
	}
	for _, transform := range transforms {
		transform(db, &h)
	}
	if e = index.BuildDerivedProjections(ctx, db, mapping); e != nil {
		t.Fatal(e)
	}
	if e = index.FinalizeGeneration(ctx, db, h); e != nil {
		t.Fatal(e)
	}
	h, e = index.ReadGenerationHeader(ctx, db, h.SnapshotID)
	if e != nil {
		t.Fatal(e)
	}
	return db, h, ids
}

// Historical partitions conserve exact bytes and every physical entry including the root directory.
func TestSourceTopologyConservation(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	ctx := context.Background()
	r := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100, Filter: Filter{Categories: []string{}}}
	out, err := Hierarchy(ctx, db, h, r)
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, out)
	counts := map[string]int{}
	var other, share string
	for _, n := range out.Nodes {
		counts[n.Kind]++
		if n.Kind == "other" {
			other = n.ID
			if n.Summary.KnownBytes != "9007199254740993" || n.Summary.DirectoryCount != "1" {
				t.Fatal(n)
			}
		}
		if n.Kind == "share" && n.Name == "Historical d" {
			share = n.ID
			if n.Summary.KnownBytes != "20" || n.Summary.UnknownFileCount != "1" {
				t.Fatal(n)
			}
		}
		if n.Virtual && (n.RealNodeID != nil || n.Classification != nil) {
			t.Fatal(n)
		}
	}
	if counts["share"] != 2 || counts["other"] != 1 {
		t.Fatal(counts)
	}
	nav, err := ReadNavigation(ctx, db, h, ids["d"], nil)
	if err != nil || nav.ParentID == nil || *nav.ParentID != share {
		t.Fatal(nav, err)
	}
	for _, root := range []string{other, share} {
		d, err := Detail(ctx, db, h, DetailRequest{SnapshotID: h.SnapshotID, RootID: "nas", NodeID: root, Metric: "logical", Operation: "details"})
		if err != nil || !d.Virtual || d.Node != nil || !ValidSourceProvenance(d.Provenance) {
			t.Fatal(d, err)
		}
		if d.Aggregate.Subtree.Logical.KnownBytes != d.Numerator.KnownBytes {
			t.Fatal(d)
		}
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "/fixture") {
		t.Fatal("private path leaked")
	}
	r.RootID = other
	r.ElementBudget = 1
	page, err := Children(ctx, db, h, r, "")
	if err != nil || len(page.Nodes) != 1 || page.Nodes[0].ID != ids["."] {
		t.Fatal(page, err)
	}
	r.RootID = ids["."]
	r.ElementBudget = 2
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err = Children(ctx, db, h, r, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range page.Nodes {
			if seen[n.ID] || n.ID == ids["d"] || n.ID == ids["empty"] {
				t.Fatal(n)
			}
			seen[n.ID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
}

// Corrected roots use projected numeric keys across page boundaries in either direction.
func TestSourceSearchProjectedPagination(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	for _, direction := range []string{"asc", "desc"} {
		r := SearchRequest{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Scope: "recursive", Sort: "size", Direction: direction, Limit: 2, Field: "both"}
		seen := map[string]bool{}
		var all []SearchItem
		cursor := ""
		for page := 0; page < 10; page++ {
			got, err := Search(context.Background(), db, h, r, cursor)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range got.Items {
				if seen[item.NodeID] {
					t.Fatalf("duplicate %s", item.NodeID)
				}
				seen[item.NodeID] = true
				all = append(all, item)
			}
			if !got.HasMore {
				break
			}
			cursor = got.NextCursor
		}
		if len(all) != 9 {
			t.Fatalf("%s count %d", direction, len(all))
		}
		var previous *SearchItem
		for i := range all {
			item := &all[i]
			if item.NodeID == ids["."] && (item.Size == nil || *item.Size != "9007199254740993") {
				t.Fatal("root size", item)
			}
			if previous != nil {
				compare := 0
				if previous.Size == nil && item.Size != nil {
					compare = -1
				} else if previous.Size != nil && item.Size == nil {
					compare = 1
				} else if previous.Size != nil {
					a, _ := new(big.Int).SetString(string(*previous.Size), 10)
					b, _ := new(big.Int).SetString(string(*item.Size), 10)
					compare = a.Cmp(b)
				}
				if direction == "asc" && compare > 0 || direction == "desc" && compare < 0 {
					t.Fatalf("out of order %s: %+v then %+v", direction, previous, item)
				}
			}
			previous = item
		}
	}
	// Other's canonical root and its descendants are scoped independently of both shares.
	other := virtualID(h.SnapshotID, "other", "volume")
	r := SearchRequest{SnapshotID: h.SnapshotID, RootID: other, Metric: "logical", Scope: "recursive", Sort: "size", Direction: "asc", Limit: 50, Field: "both"}
	got, err := Search(context.Background(), db, h, r, "")
	if err != nil || len(got.Items) != 4 {
		t.Fatal(got, err)
	}
	r.Scope = "direct"
	got, err = Search(context.Background(), db, h, r, "")
	if err != nil || len(got.Items) != 1 || got.Items[0].NodeID != ids["."] {
		t.Fatal(got, err)
	}
}

// Filtered source partitions retain both original and matching exact weights without duplicate ancestors.
func TestSourceFilteredPartitions(t *testing.T) {
	db, h, _ := sourceTopologyFixture(t)
	for _, f := range []Filter{{Text: "h1", Field: "name"}, {Text: "a.txt", Field: "name"}, {Text: "no-such-match", Field: "name"}, {ObjectKinds: []string{"directory"}}} {
		cache := projectionFixture(t, db, h, "logical", f)
		for _, mode := range []string{"highlight", "hits-only"} {
			r := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100, Filter: f, Mode: mode}
			got, err := HierarchyFiltered(context.Background(), db, cache, h, r)
			if err != nil {
				t.Fatalf("%+v %s: %v", f, mode, err)
			}
			conserved(t, got)
		}
	}
}

// Declared fixtures expose their authority explicitly and never manufacture native shares.
func TestSourceExplicitAuthority(t *testing.T) {
	for _, synthetic := range []bool{false, true} {
		db, h, _ := sourceTopologyFixture(t, func(_ *sql.DB, h *index.GenerationHeader) {
			h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, synthetic)
		})
		got, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100})
		if err != nil {
			t.Fatal(err)
		}
		conserved(t, got)
		count := 0
		for _, n := range got.Nodes {
			if n.Kind == "share" {
				t.Fatal("invented share")
			}
			if n.Kind == "other" {
				count++
				if n.Provenance == nil || n.Provenance.Qualification != "explicit" || n.Provenance.Authority == "native-inventory" {
					t.Fatal(n)
				}
			}
		}
		if count != 1 {
			t.Fatal(count)
		}
	}
}

// A nested share wrapper retains every canonical ancestor instead of flattening the physical path.
func TestSourceNestedParentContext(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t, func(db *sql.DB, h *index.GenerationHeader) {
		// Reclassifying only d as an ordinary ancestor makes d/nested the sole qualified boundary.
		id := "share-nested"
		h.Manifest.CoveredTargetIDs = []string{"volume", id}
		h.Manifest.Provenance.Roots[0].Areas = []scopemanifest.SourceArea{{SourceID: &id, VolumeID: "volume", Name: "Nested share", RelativePath: []byte("d/nested"), State: "reachable", Qualification: "qualified", Binding: &scopemanifest.RootIdentity{Path: "/fixture/d/nested", Device: "1", Inode: "256", MountID: "1"}}}
		parent := fixtureNodeID(h.SnapshotID, "volume", "d")
		nodeID := fixtureNodeID(h.SnapshotID, "volume", "d/nested")
		n := contracts.Node{SnapshotID: h.SnapshotID, ID: nodeID, ParentID: &parent, OriginalName: "nested", Kind: "directory", VolumeID: "volume", RelativePath: "d/nested"}
		raw, _ := json.Marshal(n)
		if _, err := insertQueryFixtureNode(db, nodeID, &parent, string(raw), "volume", []byte("nested"), []byte("d/nested"), "1:256:0:0"); err != nil {
			t.Fatal(err)
		}
		h.Entries++
	})
	nested := fixtureNodeID(h.SnapshotID, "volume", "d/nested")
	nav, err := ReadNavigation(context.Background(), db, h, nested, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(nav.Breadcrumb) < 3 || nav.Breadcrumb[len(nav.Breadcrumb)-2].Kind != "share" || nav.Breadcrumb[len(nav.Breadcrumb)-3].ID != ids["d"] {
		t.Fatal(nav)
	}
	got, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, got)
}

// Direct virtual-container searches return distinct real boundary representatives, never fake files.
func TestSourceDirectVolumeRepresentatives(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	for _, root := range []string{"nas", virtualID(h.SnapshotID, "volume", "volume")} {
		for _, sortBy := range []string{"name", "size", "type"} {
			r := SearchRequest{SnapshotID: h.SnapshotID, RootID: root, Scope: "direct", Field: "both", Sort: sortBy, Direction: "asc", Metric: "logical", Limit: 1}
			seen := map[string]bool{}
			cursor := ""
			sum := zero()
			for i := 0; i < 5; i++ {
				got, err := Search(context.Background(), db, h, r, cursor)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range got.Items {
					if seen[v.NodeID] {
						t.Fatal("duplicate")
					}
					seen[v.NodeID] = true
					sum, err = merge(sum, v.Summary, false)
					if err != nil {
						t.Fatal(err)
					}
				}
				if !got.HasMore {
					break
				}
				cursor = got.NextCursor
			}
			if len(seen) != 3 || !seen[ids["."]] || !seen[ids["d"]] || !seen[ids["empty"]] || sum.KnownBytes != h.LogicalBytes || sum.DirectoryCount != "3" {
				t.Fatal(root, sortBy, seen, sum)
			}
		}
	}
}

// Historical locked sources remain availability records rather than invented empty map nodes.
func TestSourceUnavailableBounded(t *testing.T) {
	db, h, _ := sourceTopologyFixture(t, func(db *sql.DB, h *index.GenerationHeader) {
		for i := 0; i < 40; i++ {
			id := fmt.Sprintf("locked-%d", i)
			h.Manifest.CoveredTargetIDs = append(h.Manifest.CoveredTargetIDs, id)
			h.Manifest.Provenance.Roots[0].Areas = append(h.Manifest.Provenance.Roots[0].Areas, scopemanifest.SourceArea{SourceID: &id, VolumeID: "volume", Name: strings.Repeat("界", 1000), RelativePath: []byte(id), State: "locked", Qualification: "unavailable"})
		}
		h.State = "partial"
		h.CoverageErrors = 1
		if _, err := db.Exec(`UPDATE coverage SET state='partial',errors=1 WHERE target_id='volume'`); err != nil {
			t.Fatal(err)
		}
	})
	got, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, got)
	a := got.SourceAvailability
	raw, _ := json.Marshal(a)
	if a.Total != "40" || !a.Truncated || len(a.Records) == 0 || len(raw) > 16<<10 {
		t.Fatal(a.Total, a.Truncated, len(raw))
	}
	for _, v := range a.Records {
		if v.Provenance.State != "locked" || v.Reason != "source-unavailable" {
			t.Fatal(v)
		}
	}
	for _, n := range got.Nodes {
		if strings.HasPrefix(n.Name, "locked-") {
			t.Fatal("invented measured source")
		}
	}
}

// Directory predicates use projected size and cannot inherit a hit into detached share partitions.
func TestSourceFilterProjectedRootPredicates(t *testing.T) {
	db, h, _ := sourceTopologyFixture(t, func(db *sql.DB, h *index.GenerationHeader) {
		if _, err := db.Exec(`UPDATE nodes SET logical_bytes='5' WHERE id=?`, fixtureNodeID(h.SnapshotID, "volume", "d/unknown.txt")); err != nil {
			t.Fatal(err)
		}
		h.LogicalBytes = "9007199254741018"
		h.UnknownLogicalEntries = 0
	})
	threshold := contracts.Bytes("9007199254740994")
	for _, tc := range []struct {
		filter             Filter
		known, files, dirs string
	}{
		{Filter{MinSize: &threshold, ObjectKinds: []string{"directory"}}, "0", "0", "0"},
		{Filter{Text: ".", Field: "name", ObjectKinds: []string{"directory"}}, "9007199254740993", "2", "1"},
	} {
		cache := projectionFixture(t, db, h, "logical", tc.filter)
		for _, mode := range []string{"highlight", "hits-only"} {
			got, err := HierarchyFiltered(context.Background(), db, cache, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100, Filter: tc.filter, Mode: mode})
			if err != nil {
				t.Fatal(err)
			}
			conserved(t, got)
			if got.MatchSummary.KnownBytes != contracts.Bytes(tc.known) || got.MatchSummary.FileCount != contracts.Bytes(tc.files) || got.MatchSummary.DirectoryCount != contracts.Bytes(tc.dirs) {
				t.Fatal(tc.filter, mode, got.MatchSummary)
			}
			page, err := Search(context.Background(), db, h, SearchRequest{SnapshotID: h.SnapshotID, RootID: "nas", Scope: "recursive", Sort: "size", Direction: "desc", Metric: "logical", Field: "both", Limit: 50, Filter: tc.filter}, "")
			if err != nil {
				t.Fatal(err)
			}
			if tc.known == "0" && len(page.Items) != 0 || tc.known != "0" && len(page.Items) != 1 {
				t.Fatal(tc.filter, page)
			}
		}
	}
}

// Distinct volumes and a share-only root conserve one NAS sum despite duplicate historical names.
func TestSourceMultipleVolumes(t *testing.T) {
	db, h, _ := sourceTopologyFixture(t, func(db *sql.DB, h *index.GenerationHeader) {
		target := "share-v2"
		volume := "volume2"
		rootID := fixtureNodeID(h.SnapshotID, target, ".")
		fileID := fixtureNodeID(h.SnapshotID, target, "item")
		size := contracts.Bytes("7")
		links := 1
		nodes := []contracts.Node{{SnapshotID: h.SnapshotID, ID: rootID, OriginalName: ".", Kind: "directory", VolumeID: volume, RelativePath: "."}, {SnapshotID: h.SnapshotID, ID: fileID, ParentID: &rootID, OriginalName: "item", Kind: "file", VolumeID: volume, RelativePath: "item", LogicalBytes: &size, AllocatedBytes: &size, LinkCount: &links}}
		for _, n := range nodes {
			n.ShareID = &target
			raw, _ := json.Marshal(n)
			if _, err := insertQueryFixtureNode(db, n.ID, n.ParentID, string(raw), target, []byte(n.OriginalName), []byte(n.RelativePath), "2:256:0:0"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO coverage VALUES(?,'complete',0)`, target); err != nil {
			t.Fatal(err)
		}
		binding := &scopemanifest.RootIdentity{Path: "/second", Device: "2", Inode: "256", MountID: "2"}
		h.Manifest.Kind = "mixed"
		h.Manifest.CoveredTargetIDs = append(h.Manifest.CoveredTargetIDs, target)
		h.Manifest.Roots = append(h.Manifest.Roots, scopemanifest.ManifestRoot{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: target, VolumeID: volume, Kind: "share", PhysicalPath: "/second"}, Binding: binding})
		h.Manifest.Provenance.Roots = append(h.Manifest.Provenance.Roots, scopemanifest.SourceRoot{TargetID: target, VolumeID: volume, VolumeName: "Historical Volume", SourceID: &target, Name: "Historical d", Kind: "share", Binding: binding, Areas: []scopemanifest.SourceArea{}})
		h.Entries += 2
		h.LogicalBytes = "9007199254741020"
	})
	got, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100})
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, got)
	volumes, shares := 0, 0
	for _, n := range got.Nodes {
		if n.Kind == "volume" {
			volumes++
		}
		if n.Kind == "share" {
			shares++
		}
	}
	if volumes != 2 || shares != 3 || got.Denominator.KnownBytes != h.LogicalBytes || got.Denominator.DirectoryCount != "4" {
		t.Fatal(volumes, shares, got.Denominator)
	}
}
