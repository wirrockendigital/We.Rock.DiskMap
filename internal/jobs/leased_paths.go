package jobs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"path/filepath"
	"time"
)

// LeasedPathsRequest names one immutable node under an already authorized reader lease.
type LeasedPathsRequest struct {
	ContractVersion int    `json:"contractVersion"`
	ProfileID       string `json:"profileId"`
	ViewID          string `json:"viewId"`
	SnapshotID      string `json:"snapshotId"`
	NodeID          string `json:"nodeId"`
}

// hierarchy shares concurrency and reader reservations without adopting the current generation.
func (r LeasedPathsRequest) hierarchy() HierarchyRequest {
	return HierarchyRequest{ContractVersion: r.ContractVersion, ProfileID: r.ProfileID, ViewID: r.ViewID, Query: query.Request{SnapshotID: r.SnapshotID, RootID: r.NodeID, Metric: "logical", Depth: 1, ElementBudget: 1}}
}

// Validate requires real node identities; virtual roots have no recorded filesystem path.
func (r LeasedPathsRequest) Validate() error {
	if !identifier.MatchString(r.NodeID) {
		return ErrInvalid
	}
	return r.hierarchy().Validate()
}

// ReadLeasedPaths reads sealed provenance only, even after a newer scan or profile revision.
func (m *Manager) ReadLeasedPaths(parent context.Context, owner string, in LeasedPathsRequest) (NodePaths, error) {
	var out NodePaths
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	release, err := m.reserveHierarchy(ctx, owner, in.hierarchy())
	if err != nil {
		return out, err
	}
	defer release()
	db, close, err := m.generationDB(in.SnapshotID)
	if err != nil {
		return out, ErrViewExpired
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, in.SnapshotID)
	if err != nil {
		return out, ErrGenerationRefresh
	}
	if header.Profile.ID != in.ProfileID {
		return out, query.ErrSnapshot
	}
	snapshotID, nodeID := in.SnapshotID, in.NodeID
	// Resolve original bytes from the sealed numeric tree before exposing a path.
	record, err := index.ReadNode(ctx, db, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, ErrUnavailable
	}
	node, target, name, path := record.Node, record.TargetID, record.Name, record.Path
	matched := 0
	for _, root := range header.Manifest.Roots {
		if root.TargetID != target {
			continue
		}
		matched++
		if root.Binding == nil || root.PhysicalPath != root.Binding.Path || !filepath.IsAbs(root.Binding.Path) || filepath.Clean(root.Binding.Path) != root.Binding.Path || security.ValidateNode(node, snapshotID, nodeID, target, root.VolumeID, string(path)) != nil {
			return out, ErrNotFound
		}
		out = NodePaths{SnapshotID: snapshotID, NodeID: nodeID, Name: pathText(string(name)), NASPath: pathText(filepath.Join(root.Binding.Path, string(path)))}
	}
	if matched != 1 {
		return NodePaths{}, ErrNotFound
	}
	return out, nil
}
