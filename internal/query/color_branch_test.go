package query

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestColorBranchIdentity binds raw historical source identity independently of snapshot and scan scope.
func TestColorBranchIdentity(t *testing.T) {
	_, h, _ := sourceTopologyFixture(t)
	e := engine{h: h}
	key := func(snapshot, target, raw, kind string) *string {
		e.h.SnapshotID = snapshot
		return e.colorBranchKeyForTarget(contracts.Node{ID: fixtureNodeID(snapshot, target, raw), VolumeID: "volume", Kind: kind}, []byte(raw), target)
	}
	first := key("old", "volume", "d/sub/file", "file")
	next := key("new", "volume", "d/sub/file", "file")
	directory := key("new", "volume", "d/sub", "directory")
	if first == nil || next == nil || directory == nil || *first != *next || *first != *directory {
		t.Fatal("snapshot/containing-folder identity changed")
	}
	for _, raw := range []string{"d/other/file", "d/sub\xff/file", "d/sub\ufffd/file"} {
		v := key("new", "volume", raw, "file")
		if v == nil || *v == *first {
			t.Fatal("different raw branch collided")
		}
	}
	oldRoot := e.h.Manifest.Provenance.Roots[0]
	sid := "share-d"
	e.h.Manifest.Provenance.Roots = []scopemanifest.SourceRoot{{TargetID: sid, VolumeID: "volume", SourceID: &sid, Kind: "share", Binding: oldRoot.Areas[0].Binding}}
	share := key("third", sid, "sub/file", "file")
	if share == nil || *share != *first {
		t.Fatal("share and volume scope disagree")
	}
	e.h.Manifest.Provenance.Roots[0].SourceID = &oldRoot.TargetID
	changed := key("third", sid, "sub/file", "file")
	if changed == nil || *changed == *first {
		t.Fatal("different source reused key")
	}
	e.h.Manifest.Provenance.Authority = "explicit-roots"
	if key("third", sid, "sub/file", "file") != nil {
		t.Fatal("explicit root invented native color authority")
	}
	e.h.Manifest.Provenance.Authority = "native-inventory"
	e.h.Manifest.Provenance.Roots[0].Binding = nil
	if key("third", sid, "sub/file", "file") != nil {
		t.Fatal("unbound root invented authority")
	}
}

// TestColorBranchHierarchy checks that virtual wrappers inherit only qualified canonical metadata.
func TestColorBranchHierarchy(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	request := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 8, ElementBudget: 100, Filter: Filter{Categories: []string{}}}
	full, err := Hierarchy(context.Background(), db, h, request)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]Node{}
	for _, n := range full.Nodes {
		found[n.ID] = n
		if n.Kind == "nas" && n.ColorBranchKey != nil {
			t.Fatal("unknown wrapper fabricated key")
		}
	}
	for _, n := range full.Nodes {
		if n.Kind == "volume" && n.ColorBranchKey == nil {
			t.Fatal("qualified volume missing key")
		}
	}
	folder := found[ids["d"]]
	if folder.ColorBranchKey == nil {
		t.Fatal("qualified folder missing color")
	}
	request.RootID = ids["d"]
	drill, err := Hierarchy(context.Background(), db, h, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range drill.Nodes {
		old := found[n.ID]
		if n.Summary != old.Summary {
			t.Fatal("drill changed counters")
		}
		if n.ColorBranchKey == nil || old.ColorBranchKey == nil || *n.ColorBranchKey != *old.ColorBranchKey {
			t.Fatal("drill changed color")
		}
	}
	for _, n := range full.Nodes {
		if n.Kind == "share" && n.Name == "Historical d" {
			if n.ColorBranchKey == nil || *n.ColorBranchKey != *folder.ColorBranchKey {
				t.Fatal("qualified wrapper differs")
			}
		}
	}
}

// TestColorBranchSealedSnapshots compares actual query output from separately sealed generations.
func TestColorBranchSealedSnapshots(t *testing.T) {
	first, h1, _ := sourceTopologyFixture(t)
	second, h2, _ := sourceTopologyFixture(t, func(db *sql.DB, h *index.GenerationHeader) {
		h.SnapshotID = "second-snapshot"
		// Snapshot membership is stored once; numeric locators remain local to each generation.
		if _, err := db.Exec(`UPDATE generation_identity SET snapshot_id=?`, h.SnapshotID); err != nil {
			t.Fatal(err)
		}
	})
	for _, metric := range []string{"logical", "allocated"} {
		req := Request{SnapshotID: h1.SnapshotID, RootID: "nas", Metric: metric, Depth: 8, ElementBudget: 100, Filter: Filter{Categories: []string{}}}
		a, err := Hierarchy(context.Background(), first, h1, req)
		if err != nil {
			t.Fatal(err)
		}
		req.SnapshotID = h2.SnapshotID
		b, err := Hierarchy(context.Background(), second, h2, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Nodes) != len(b.Nodes) {
			t.Fatal("different topology")
		}
		byName := map[string]Node{}
		for _, n := range a.Nodes {
			byName[n.Kind+":"+n.Name] = n
		}
		for _, n := range b.Nodes {
			old, ok := byName[n.Kind+":"+n.Name]
			if !ok || old.Summary != n.Summary {
				t.Fatal("changed counters")
			}
			if (old.ColorBranchKey == nil) != (n.ColorBranchKey == nil) {
				t.Fatal("changed qualification")
			}
			if n.ColorBranchKey != nil && *n.ColorBranchKey != *old.ColorBranchKey {
				t.Fatal("changed snapshot color")
			}
			if !n.Virtual && old.ID != n.ID {
				t.Fatal("generation-local numeric locator unexpectedly changed")
			}
		}
	}
}
