package jobs

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"time"
)

// SizeMetricsRequest carries only existing reader identities, never qualification flags or source paths.
type SizeMetricsRequest struct {
	ContractVersion int    `json:"contractVersion"`
	ProfileID       string `json:"profileId"`
	ViewID          string `json:"viewId"`
	SnapshotID      string `json:"snapshotId"`
	RootID          string `json:"rootId"`
}

// hierarchy reuses the established principal, lease, snapshot and concurrency boundary.
func (r SizeMetricsRequest) hierarchy() HierarchyRequest {
	return HierarchyRequest{ContractVersion: r.ContractVersion, ProfileID: r.ProfileID, ViewID: r.ViewID, Query: query.Request{SnapshotID: r.SnapshotID, RootID: r.RootID, Metric: "logical", Depth: 1, ElementBudget: 1}}
}

// Validate rejects malformed identities before metadata discovery.
func (r SizeMetricsRequest) Validate() error { return r.hierarchy().Validate() }

// ReadSizeMetrics preserves the pinned generation while sampling separate current volume metadata.
func (m *Manager) ReadSizeMetrics(parent context.Context, owner string, in SizeMetricsRequest) (query.SizeMetricsResponse, error) {
	ctx, cancel := context.WithTimeout(parent, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	release, err := m.reserveHierarchy(ctx, owner, in.hierarchy())
	if err != nil {
		return query.SizeMetricsResponse{}, err
	}
	defer release()
	db, close, err := m.generationDB(in.SnapshotID)
	if err != nil {
		return query.SizeMetricsResponse{}, ErrViewExpired
	}
	defer close()
	h, err := index.ReadGenerationHeader(ctx, db, in.SnapshotID)
	if err != nil {
		return query.SizeMetricsResponse{}, err
	}
	if h.Profile.ID != in.ProfileID {
		return query.SizeMetricsResponse{}, query.ErrSnapshot
	}
	// Resolve the selected root from the sealed index before matching current inventory identity.
	root, err := query.Hierarchy(ctx, db, h, query.Request{SnapshotID: in.SnapshotID, RootID: in.RootID, Metric: "logical", Depth: 1, ElementBudget: 1})
	if err != nil {
		return query.SizeMetricsResponse{}, err
	}
	var volume *contracts.Volume
	discovered, e := inventory.Discover(ctx, nil)
	if e == nil && len(discovered.Volumes) <= 512 {
		for _, v := range discovered.Volumes {
			if v.ID == root.Nodes[0].VolumeID {
				copy := v
				volume = &copy
				break
			}
		}
	}
	if ctx.Err() != nil {
		return query.SizeMetricsResponse{}, ctx.Err()
	}
	return query.SizeMetrics(ctx, db, h, in.RootID, volume)
}
