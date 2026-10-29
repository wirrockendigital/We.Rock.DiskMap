package query

import (
	"context"
	"encoding/json"
	"testing"
)

// Schema13 reports unavailable timestamps and rejects predicates that cannot be evaluated.
func TestHierarchyRecordedModifiedAt(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	for _, metric := range []string{"logical", "allocated"} {
		out, err := Hierarchy(context.Background(), db, h, Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: metric, Depth: 8, ElementBudget: 2000})
		if err != nil {
			t.Fatal(err)
		}
		conserved(t, out)
		for _, n := range out.Nodes {
			raw, _ := json.Marshal(n)
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			if n.ModifiedAt != nil || string(fields["modifiedAt"]) != "null" {
				t.Fatal("invented timestamp")
			}
		}
	}
	stamp := "2020-01-01T00:00:00Z"
	for _, f := range []Filter{{ModifiedFrom: &stamp}, {ModifiedTo: &stamp}} {
		if _, err := f.Canonical(); err == nil {
			t.Fatal("unsupported timestamp accepted")
		}
	}
	r := baseSearch(h)
	r.Sort = "mtime"
	if _, err := r.Validate(); err == nil {
		t.Fatal("unsupported sort accepted")
	}
}
