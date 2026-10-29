package query

import (
	"context"
	"database/sql"
	"math/big"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// AllocatedQualification is immutable method provenance, not a claim about exclusive or reclaimable blocks.
const AllocatedQualification = contracts.AllocatedQualification

// SizeMetric exposes known subtotals and unknown regular files without replacing missing data with zero.
type SizeMetric struct {
	Selectable       bool             `json:"selectable"`
	KnownBytes       *contracts.Bytes `json:"knownBytes"`
	UnknownFileCount *contracts.Bytes `json:"unknownFileCount"`
	Method           *string          `json:"method"`
	Reason           *string          `json:"reason"`
	Qualification    *string          `json:"qualification"`
}

// SizeCapture identifies the immutable observation interval independently from volume sampling.
type SizeCapture struct {
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
}

// SizeVolume deliberately excludes private inventory paths and identity evidence.
type SizeVolume struct {
	VolumeID  string                `json:"volumeId"`
	Capacity  contracts.Measurement `json:"capacity"`
	Used      contracts.Measurement `json:"used"`
	Available contracts.Measurement `json:"available"`
}

// SizeComparison describes arithmetic only; it never assigns a difference to snapshots or deletion savings.
type SizeComparison struct {
	Delta        *contracts.SignedBytes `json:"delta"`
	Reason       string                 `json:"reason"`
	Basis        string                 `json:"basis"`
	VolumeID     *string                `json:"volumeId"`
	MeasuredAt   *string                `json:"measuredAt"`
	CaptureStart string                 `json:"captureStart"`
	CaptureEnd   string                 `json:"captureEnd"`
}

// SizeMetricsResponse is a version-1 envelope payload with explicit provenance and availability.
type SizeMetricsResponse struct {
	SnapshotID    string         `json:"snapshotId"`
	RootID        string         `json:"rootId"`
	DefaultMetric string         `json:"defaultMetric"`
	Capture       SizeCapture    `json:"capture"`
	Logical       SizeMetric     `json:"logical"`
	Allocated     SizeMetric     `json:"allocated"`
	Volume        *SizeVolume    `json:"volume"`
	Comparison    SizeComparison `json:"comparison"`
}

// SizeMetrics reads only sealed index summaries; metric changes never schedule source traversal.
func SizeMetrics(ctx context.Context, db *sql.DB, h index.GenerationHeader, rootID string, volume *contracts.Volume) (SizeMetricsResponse, error) {
	request := Request{SnapshotID: h.SnapshotID, RootID: rootID, Metric: "logical", Depth: 1, ElementBudget: 1}
	logical, err := Hierarchy(ctx, db, h, request)
	if err != nil {
		return SizeMetricsResponse{}, err
	}
	request.Metric = "allocated"
	allocated, err := Hierarchy(ctx, db, h, request)
	if err != nil {
		return SizeMetricsResponse{}, err
	}
	// Only the exact recorded scope-root target can establish a whole-volume basis.
	var target string
	var rawPath []byte
	if !logical.Nodes[0].Virtual {
		stored, err := index.ReadNode(ctx, db, logical.Nodes[0].ID)
		if err != nil {
			return SizeMetricsResponse{}, err
		}
		target, rawPath = stored.TargetID, stored.Path
		if string(rawPath) != "." {
			target = ""
		}
	}
	// A partitioned canonical root now measures Other, not the complete volume namespace.
	projector := &engine{ctx: ctx, db: db, h: h, r: request, now: time.Now}
	if err := projector.topology(); err != nil {
		return SizeMetricsResponse{}, err
	}
	if len(projector.excluded[logical.Nodes[0].ID]) > 0 {
		target = ""
	}
	return sizeMetrics(h, logical.Nodes[0], allocated.Nodes[0], target, volume)
}

// sizeMetrics accepts only same-root projections produced by the bound query above.
func sizeMetrics(h index.GenerationHeader, l, a Node, target string, v *contracts.Volume) (SizeMetricsResponse, error) {
	out := SizeMetricsResponse{SnapshotID: h.SnapshotID, RootID: l.ID, DefaultMetric: "logical", Capture: SizeCapture{h.StartedAt, h.EndedAt}}
	start, e1 := time.Parse(time.RFC3339Nano, h.StartedAt)
	end, e2 := time.Parse(time.RFC3339Nano, h.EndedAt)
	if e1 != nil || e2 != nil || end.Before(start) || l.ID != a.ID || l.VolumeID != a.VolumeID {
		return out, ErrSnapshot
	}
	for _, s := range []Summary{l.Summary, a.Summary} {
		if _, err := merge(zero(), s, false); err != nil {
			return out, err
		}
	}
	logicalMethod := "st_size"
	out.Logical = SizeMetric{Selectable: true, KnownBytes: &l.Summary.KnownBytes, UnknownFileCount: &l.Summary.UnknownFileCount, Method: &logicalMethod}
	reason := "METHOD_UNQUALIFIED"
	out.Allocated = SizeMetric{Reason: &reason}
	qualified := false
	for _, m := range h.Methods {
		if m == AllocatedQualification {
			qualified = true
		}
	}
	if qualified {
		method := "st_blocks*512"
		q := AllocatedQualification
		out.Allocated = SizeMetric{Selectable: true, KnownBytes: &a.Summary.KnownBytes, UnknownFileCount: &a.Summary.UnknownFileCount, Method: &method, Qualification: &q}
	}
	out.Comparison = SizeComparison{Reason: "VOLUME_UNAVAILABLE", Basis: "volume-used-minus-recorded-allocated", CaptureStart: h.StartedAt, CaptureEnd: h.EndedAt}
	if v == nil || v.ID != l.VolumeID {
		return out, nil
	}
	out.Volume = &SizeVolume{v.ID, v.Capacity, v.Used, v.Available}
	out.Comparison.VolumeID = &out.Volume.VolumeID
	out.Comparison.MeasuredAt = &out.Volume.Used.MeasuredAt
	// An explicit complete volume root is required; a share or selected subtree is not a volume total.
	whole := false
	for _, r := range h.Manifest.Roots {
		if r.Kind == "volume" && r.TargetID == target && r.VolumeID == v.ID && !l.Virtual && l.Kind == "directory" && l.ParentID == nil {
			whole = true
		}
	}
	if !whole || h.State != "complete" || h.CoverageErrors != 0 || len(h.Manifest.Issues) != 0 || len(h.Manifest.Exclusions) != 0 || len(h.Profile.Exclusions) != 0 || h.ScanBoundaryCount != "0" {
		out.Comparison.Reason = "SCOPE_INCOMPARABLE"
		return out, nil
	}
	if !qualified {
		out.Comparison.Reason = "METHOD_UNQUALIFIED"
		return out, nil
	}
	if v.State != "reachable" || v.Used.Source != "linux.fstatfs" || v.Used.Method != "fstatfs.(blocks-bfree)*frsize" {
		out.Comparison.Reason = "METHOD_UNQUALIFIED"
		return out, nil
	}
	measured, err := time.Parse(time.RFC3339Nano, v.Used.MeasuredAt)
	if err != nil || measured.Before(start) || measured.After(end) {
		out.Comparison.Reason = "TIME_INCOMPARABLE"
		return out, nil
	}
	if v.Used.Value == nil || a.Summary.UnknownFileCount != "0" || v.Used.Value.Validate() != nil || len(*v.Used.Value) > 128 {
		out.Comparison.Reason = "UNKNOWN_VALUES"
		return out, nil
	}
	used, _ := new(big.Int).SetString(string(*v.Used.Value), 10)
	sum, _ := new(big.Int).SetString(string(a.Summary.KnownBytes), 10)
	delta := contracts.SignedBytes(used.Sub(used, sum).String())
	if delta.Validate() != nil {
		return out, ErrSnapshot
	}
	out.Comparison.Delta = &delta
	out.Comparison.Reason = "COMPARABLE"
	return out, nil
}
