package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"reflect"
	"strings"
	"testing"
)

// TestRecordedClassification uses only immutable raw metadata, including conflicting object kinds.
func TestRecordedClassification(t *testing.T) {
	mapping := index.DefaultFileTypeMapping()
	mapping.Revision = 7
	mapping.Assignments = append(mapping.Assignments, contracts.ExtensionMapping{Extension: "图", Category: "image"})
	tests := []struct{ name, want, basis string }{{"PHOTO.JpG", "image", "extension"}, {"clip.MP4", "video", "extension"}, {"sound.flac", "audio", "extension"}, {"paper.pdf", "document", "extension"}, {"backup.tar.GZ", "archive", "extension"}, {"volume.ISO", "diskImage", "extension"}, {"data.sqlite", "database", "extension"}, {"noextension", "otherUnknown", "unknown"}, {".jpg", "otherUnknown", "unknown"}, {"file.", "otherUnknown", "unknown"}, {"x.unmapped", "otherUnknown", "unknown"}, {"bad\xff.jpg", "otherUnknown", "unknown"}, {"picture.图", "image", "extension"}, {".hidden.jpg", "image", "extension"}, {"report.pdf.exe", "otherUnknown", "unknown"}}
	for _, tc := range tests {
		n := contracts.Node{Kind: "file", OriginalName: "masked-display.jpg", RelativePath: "display"}
		got, e := classifyRecorded(n, []byte(tc.name), []byte("docker/"+tc.name), sql.NullString{}, mapping)
		if e != nil || got.Category == nil || *got.Category != tc.want || got.Basis != tc.basis || got.MappingRevision != 7 || len(got.ContextHints) != 1 {
			t.Fatal(tc, got, e)
		}
	}
	for _, kind := range []string{"symlink", "fifo", "socket", "blockDevice", "characterDevice"} {
		got, e := classifyRecorded(contracts.Node{Kind: kind}, []byte("picture.jpg"), []byte("picture.jpg"), sql.NullString{}, mapping)
		if e != nil || got.Category != nil || got.Basis != "not-applicable" {
			t.Fatal(kind, got, e)
		}
	}
	for _, raw := range []string{"docker", "mydocker/photo.jpg", "dockerfile/photo.jpg", "Docker/photo.jpg", "x/do\xffcker/photo.jpg"} {
		got, e := classifyRecorded(contracts.Node{Kind: "file"}, []byte("photo.jpg"), []byte(raw), sql.NullString{}, mapping)
		if e != nil || len(got.ContextHints) != 0 {
			t.Fatal(raw, got, e)
		}
	}
}

// TestFolderClassification describes the original sealed composition without assigning the folder a file type.
func TestFolderClassification(t *testing.T) {
	mapping := index.DefaultFileTypeMapping()
	for _, tc := range []struct {
		types map[string]contracts.TypeAggregate
		want  string
	}{{nil, "no-regular-files"}, {map[string]contracts.TypeAggregate{"image": {FileCount: "2"}}, "single-category"}, {map[string]contracts.TypeAggregate{"image": {FileCount: "2"}, "document": {FileCount: "1"}, "otherUnknown": {FileCount: "1"}}, "mixed"}} {
		metric := contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}
		content := contracts.ContentAggregate{FileCount: "0", DirectoryCount: "0", SpecialCount: "0", Logical: metric, Allocated: metric, Types: tc.types}
		for key, v := range content.Types {
			v.Logical = metric
			v.Allocated = metric
			content.Types[key] = v
		}
		raw, err := json.Marshal(contracts.DirectoryAggregate{Direct: content, Subtree: content})
		if err != nil {
			t.Fatal(err)
		}
		got, e := classifyRecorded(contracts.Node{Kind: "directory"}, []byte("folder.jpg"), []byte("docker/folder.jpg"), sql.NullString{Valid: true, String: string(raw)}, mapping)
		if e != nil || got.Category != nil || got.Basis != "not-applicable" || got.FolderComposition != tc.want {
			t.Fatal(got, e)
		}
		if tc.want == "mixed" && !reflect.DeepEqual(got.FolderCategories, []string{"document", "image", "otherUnknown"}) {
			t.Fatal(got)
		}
	}
}

// TestClassificationPublicQueries binds hierarchy and search to the same frozen mapping and mtime.
func TestClassificationPublicQueries(t *testing.T) {
	db, h, ids := filterFixture(t)
	ctx := context.Background()
	r := Request{SnapshotID: h.SnapshotID, RootID: ids["."], Metric: "logical", Depth: 8, ElementBudget: 100}
	out, e := Hierarchy(ctx, db, h, r)
	if e != nil {
		t.Fatal(e)
	}
	found := map[string]*Classification{}
	for _, n := range out.Nodes {
		found[n.ID] = n.Classification
	}
	if found[ids["folder"]].FolderComposition != "mixed" || found[ids["folder"]].Category != nil || *found[ids["folder/a.jpg"]].Category != "image" || found[ids["folder/link"]].Category != nil {
		t.Fatal(found)
	}
	sr := baseSearch(h)
	sr.RootID = ids["."]
	sr.Text = "a.jpg"
	page, e := Search(ctx, db, h, sr, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	item := page.Items[0]
	if !reflect.DeepEqual(item.Classification, found[item.NodeID]) || item.ModifiedAt != nil {
		t.Fatal(item)
	}
	encoded, _ := json.Marshal(item)
	if strings.Contains(string(encoded), "lastUsed") || strings.Contains(string(encoded), "accessedAt") {
		t.Fatal("invented usage timestamp")
	}
	// A caller cannot substitute current settings for this generation's captured mapping.
	changed := h
	changed.FileTypeMapping = index.DefaultFileTypeMapping()
	changed.FileTypeMapping.Revision++
	if _, e = Hierarchy(ctx, db, changed, r); !errors.Is(e, ErrSnapshot) {
		t.Fatal(e)
	}
	// Filtered subsets must not turn an originally mixed folder into a claimed uniform folder type.
	r.Filter = Filter{Categories: []string{"image"}}
	r.Mode = "hits-only"
	cache := projectionFixture(t, db, h, r.Metric, r.Filter)
	filtered, e := HierarchyFiltered(ctx, db, cache, h, r)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range filtered.Nodes {
		if n.ID == ids["folder"] && n.Classification.FolderComposition != "mixed" {
			t.Fatal(n)
		}
	}
}
