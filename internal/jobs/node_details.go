package jobs

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"path/filepath"
	"time"
)

// NodeDetailsRequest binds historical metadata and rankings to one existing authenticated view.
type NodeDetailsRequest struct {
	ContractVersion int    `json:"contractVersion"`
	ProfileID       string `json:"profileId"`
	ViewID          string `json:"viewId"`
	SnapshotID      string `json:"snapshotId"`
	RootID          string `json:"rootId"`
	NodeID          string `json:"nodeId"`
	Metric          string `json:"metric"`
	Operation       string `json:"operation"`
	Cursor          string `json:"cursor,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

// query preserves the flat strict public schema without anonymous-field decoder ambiguity.
func (r NodeDetailsRequest) query() query.DetailRequest {
	return query.DetailRequest{SnapshotID: r.SnapshotID, RootID: r.RootID, NodeID: r.NodeID, Metric: r.Metric, Operation: r.Operation, Cursor: r.Cursor, Limit: r.Limit}
}

// Validate shares the immutable reader grammar while retaining distinct ranking selectors.
func (r NodeDetailsRequest) Validate() error {
	if r.query().Validate() != nil {
		return ErrInvalid
	}
	return r.hierarchy().Validate()
}
func (r NodeDetailsRequest) hierarchy() HierarchyRequest {
	return HierarchyRequest{ContractVersion: r.ContractVersion, ProfileID: r.ProfileID, ViewID: r.ViewID, Query: query.Request{SnapshotID: r.SnapshotID, RootID: r.RootID, Metric: r.Metric, Depth: 1, ElementBudget: 1}}
}

// NodeDetails keeps historical copied paths separate from any live File Station action.
type NodeDetails struct {
	ProfileID         string               `json:"profileId"`
	ViewID            string               `json:"viewId"`
	Detail            query.DetailResponse `json:"detail"`
	Paths             *NodePaths           `json:"paths"`
	PathProvenance    string               `json:"pathProvenance"`
	FileStationReason string               `json:"fileStationReason"`
}

// ReadNodeDetails reserves cleanup protection before opening the exact sealed generation.
func (m *Manager) ReadNodeDetails(parent context.Context, owner string, in NodeDetailsRequest) (NodeDetails, error) {
	var out NodeDetails
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	release, e := m.reserveHierarchy(ctx, owner, in.hierarchy())
	if e != nil {
		return out, e
	}
	defer release()
	db, close, e := m.generationDB(in.SnapshotID)
	if e != nil {
		return out, ErrViewExpired
	}
	defer close()
	h, e := index.ReadGenerationHeader(ctx, db, in.SnapshotID)
	if e != nil {
		return out, ErrGenerationRefresh
	}
	if h.Profile.ID != in.ProfileID {
		return out, query.ErrSnapshot
	}
	detail, e := query.Detail(ctx, db, h, in.query())
	if e != nil {
		return out, e
	}
	out = NodeDetails{ProfileID: in.ProfileID, ViewID: in.ViewID, Detail: detail, PathProvenance: "not-applicable", FileStationReason: "VIRTUAL_NODE"}
	if !detail.Virtual {
		// Reuse the historical path contract under this already held reader reservation.
		// A second reservation would needlessly consume both global query slots.
		record, readErr := index.ReadNode(ctx, db, detail.NodeID)
		if readErr != nil {
			return NodeDetails{}, ErrNotFound
		}
		target, name, path := record.TargetID, record.Name, record.Path
		var paths NodePaths
		matched := 0
		for _, root := range h.Manifest.Roots {
			if root.TargetID != target {
				continue
			}
			matched++
			if root.Binding == nil || root.PhysicalPath != root.Binding.Path || !filepath.IsAbs(root.Binding.Path) || filepath.Clean(root.Binding.Path) != root.Binding.Path || security.ValidateNode(*detail.Node, in.SnapshotID, detail.NodeID, target, root.VolumeID, string(path)) != nil {
				return NodeDetails{}, ErrNotFound
			}
			paths = NodePaths{SnapshotID: in.SnapshotID, NodeID: detail.NodeID, Name: pathText(string(name)), NASPath: pathText(filepath.Join(root.Binding.Path, string(path)))}
		}
		if matched != 1 {
			return NodeDetails{}, ErrNotFound
		}

		out.Paths = &paths
		out.PathProvenance = "snapshot"
		out.FileStationReason = "HISTORICAL_MAPPING_NOT_RECORDED"
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > query.MaxDetailResponseBytes-2048 {
		return NodeDetails{}, query.ErrBudget
	}
	return out, nil
}
