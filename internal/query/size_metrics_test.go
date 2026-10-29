package query

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"strings"
	"testing"
)

// sizeFixture supplies a hand-calculated full-volume arithmetic basis independently of query SQL.
func sizeFixture() (index.GenerationHeader, Node, Node, contracts.Volume) {
	h := index.GenerationHeader{SnapshotID: "size-snapshot", Methods: []string{"descriptor-no-follow-stat", AllocatedQualification}, StartedAt: "2026-10-10T01:00:00Z", EndedAt: "2026-10-10T01:05:00Z", State: "complete", ScanBoundaryCount: "0", Manifest: scopemanifest.ScopeManifest{Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume1", VolumeID: "volume1", Kind: "volume"}}}}}
	l := Node{Kind: "directory", ID: fixtureNodeID(h.SnapshotID, "volume1", "."), VolumeID: "volume1", Summary: zero()}
	l.Summary.KnownBytes = "9007199254740993"
	l.Summary.FileCount = "4"
	a := l
	a.Summary.KnownBytes = "9007199254741003"
	used := contracts.Bytes("9007199254740993")
	v := contracts.Volume{ID: "volume1", State: "reachable", Used: contracts.Measurement{Value: &used, Source: "linux.fstatfs", MeasuredAt: "2026-10-10T01:03:00Z", Method: "fstatfs.(blocks-bfree)*frsize"}}
	return h, l, a, v
}

// TestSizeComparison preserves negative differences above 2^53 and exposes their exact limited meaning.
func TestSizeComparison(t *testing.T) {
	h, l, a, v := sizeFixture()
	out, err := sizeMetrics(h, l, a, "volume1", &v)
	if err != nil || out.Comparison.Delta == nil || *out.Comparison.Delta != "-10" || out.Comparison.Reason != "COMPARABLE" {
		t.Fatal(out, err)
	}
	if out.DefaultMetric != "logical" || !out.Allocated.Selectable || *out.Logical.KnownBytes != "9007199254740993" {
		t.Fatal(out)
	}
	raw, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(raw), `"delta":"-10"`) {
		t.Fatal(string(raw), err)
	}
	var roundtrip SizeMetricsResponse
	if err = json.Unmarshal(raw, &roundtrip); err != nil || *roundtrip.Comparison.Delta != "-10" {
		t.Fatal(err)
	}
	// A path-counted hardlink/reflink sum may exceed volume used without being clamped or deduplicated.
	a.Summary.KnownBytes = "9007199254741993"
	out, err = sizeMetrics(h, l, a, "volume1", &v)
	if err != nil || *out.Comparison.Delta != "-1000" {
		t.Fatal(out, err)
	}
	// Sparse regular files can have known zero allocation, which is different from unavailable allocation.
	a.Summary.KnownBytes = "0"
	out, err = sizeMetrics(h, l, a, "volume1", &v)
	if err != nil || !out.Allocated.Selectable || *out.Allocated.KnownBytes != "0" {
		t.Fatal(out, err)
	}
}

// TestSizeComparisonFailClosed never invents a comparable scope, time, method or missing measurement.
func TestSizeComparisonFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*index.GenerationHeader, *Node, *Node, *contracts.Volume)
	}{
		{"old-method", "METHOD_UNQUALIFIED", func(h *index.GenerationHeader, _, _ *Node, _ *contracts.Volume) { h.Methods = h.Methods[:1] }},
		{"share-only", "SCOPE_INCOMPARABLE", func(h *index.GenerationHeader, _, _ *Node, _ *contracts.Volume) { h.Manifest.Roots[0].Kind = "share" }},
		{"partial", "SCOPE_INCOMPARABLE", func(h *index.GenerationHeader, _, _ *Node, _ *contracts.Volume) { h.State = "partial" }},
		{"excluded", "SCOPE_INCOMPARABLE", func(h *index.GenerationHeader, _, _ *Node, _ *contracts.Volume) { h.ScanBoundaryCount = "1" }},
		{"old-time", "TIME_INCOMPARABLE", func(_ *index.GenerationHeader, _, _ *Node, v *contracts.Volume) {
			v.Used.MeasuredAt = "2026-10-09T01:03:00Z"
		}},
		{"new-time", "TIME_INCOMPARABLE", func(_ *index.GenerationHeader, _, _ *Node, v *contracts.Volume) {
			v.Used.MeasuredAt = "2026-10-11T01:03:00Z"
		}},
		{"unknown", "UNKNOWN_VALUES", func(_ *index.GenerationHeader, _, a *Node, _ *contracts.Volume) { a.Summary.UnknownFileCount = "1" }},
		{"nil-volume-used", "UNKNOWN_VALUES", func(_ *index.GenerationHeader, _, _ *Node, v *contracts.Volume) { v.Used.Value = nil }},
		{"wrong-source", "METHOD_UNQUALIFIED", func(_ *index.GenerationHeader, _, _ *Node, v *contracts.Volume) { v.Used.Source = "unverified" }},
		{"wrong-volume", "VOLUME_UNAVAILABLE", func(_ *index.GenerationHeader, _, _ *Node, v *contracts.Volume) { v.ID = "other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, l, a, v := sizeFixture()
			tc.change(&h, &l, &a, &v)
			out, err := sizeMetrics(h, l, a, "volume1", &v)
			if err != nil || out.Comparison.Delta != nil || out.Comparison.Reason != tc.reason {
				t.Fatal(out, err)
			}
			if tc.name == "old-method" && (out.Allocated.Selectable || out.Allocated.KnownBytes != nil || out.Allocated.Method != nil) {
				t.Fatal("historical method invented")
			}
		})
	}
}

// TestSizeMetricsSealedRead uses real sealed query fixtures and keeps legacy unknown qualification explicit.
func TestSizeMetricsSealedRead(t *testing.T) {
	db, h, ids := filterFixture(t)
	out, err := SizeMetrics(context.Background(), db, h, ids["."], nil)
	if err != nil || *out.Logical.KnownBytes != "9007199254741023" || *out.Logical.UnknownFileCount != "1" || out.Allocated.Selectable {
		t.Fatal(out, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = SizeMetrics(ctx, db, h, ids["."], nil); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err = SizeMetrics(context.Background(), db, h, "foreign-root", nil); err == nil {
		t.Fatal("foreign root accepted")
	}
}
