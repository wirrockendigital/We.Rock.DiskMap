package query

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"reflect"
	"testing"
)

// Empty literals make name-only and path-capable traversal independent equivalent
// oracles, including projected roots, excluded shares, direct scope and cursor pages.
func TestNumericNameScopeParity(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	roots := []string{"nas", virtualID(h.SnapshotID, "volume", "volume"), virtualID(h.SnapshotID, "other", "volume"), ids["."], ids["d"]}
	for _, root := range roots {
		for _, scope := range []string{"direct", "recursive"} {
			for _, metric := range []string{"logical", "allocated"} {
				for _, order := range []string{"name", "size", "type"} {
					for _, direction := range []string{"asc", "desc"} {
						r := SearchRequest{SnapshotID: h.SnapshotID, RootID: root, Scope: scope, Metric: metric, Sort: order, Direction: direction, Limit: 2, Field: "name"}
						a, b := "", ""
						for page := 0; page < 10; page++ {
							got, err := Search(context.Background(), db, h, r, a)
							if err != nil {
								t.Fatal(root, scope, metric, order, direction, err)
							}
							reference := r
							reference.Field = "both"
							want, err := Search(context.Background(), db, h, reference, b)
							if err != nil {
								t.Fatal(err)
							}
							if !reflect.DeepEqual(got.Items, want.Items) || got.HasMore != want.HasMore {
								t.Fatalf("root=%s scope=%s metric=%s sort=%s/%s page=%d got=%+v want=%+v", root, scope, metric, order, direction, page, got, want)
							}
							if !got.HasMore {
								break
							}
							a, b = got.NextCursor, want.NextCursor
							if page == 9 {
								t.Fatal("cursor stalled")
							}
						}
					}
				}
			}
		}
	}
}

// Captured whole-target partitions with excluded shares must use subtree-aware membership.
func TestNumericNameExcludedSubtreesAndFilters(t *testing.T) {
	db, h, ids := sourceTopologyFixture(t)
	r := SearchRequest{SnapshotID: h.SnapshotID, RootID: virtualID(h.SnapshotID, "other", "volume"), Scope: "recursive", Metric: "logical", Sort: "size", Direction: "desc", Limit: 50, Field: "name"}
	scoped, err := resolveSearchScope(context.Background(), db, h, r)
	if err != nil || len(scoped.excludedIDs) == 0 || scoped.targets != nil || scoped.parent != ids["."] {
		t.Fatalf("partition is not ancestry-qualified: %v", err)
	}
	minimum := contracts.Bytes("1")
	for _, filter := range []Filter{{}, {Categories: []string{"document"}}, {ObjectKinds: []string{"file"}}, {MinSize: &minimum}, {Text: "a", Field: "name"}, {Text: "a", Field: "path"}} {
		r.Filter = filter
		got, err := Search(context.Background(), db, h, r, "")
		if err != nil {
			t.Fatal(err)
		}
		reference := r
		reference.Field = "both"
		want, err := Search(context.Background(), db, h, reference, "")
		if err != nil || !reflect.DeepEqual(got.Items, want.Items) {
			t.Fatal("filtered excluded scope differs", err)
		}
		for _, item := range got.Items {
			if item.NodeID == ids["d"] || item.NodeID == ids["d/h1.bin"] || item.NodeID == ids["d/h2.bin"] || item.NodeID == ids["d/unknown.txt"] || item.NodeID == ids["empty"] {
				t.Fatal("excluded subtree leaked")
			}
		}
	}
}
