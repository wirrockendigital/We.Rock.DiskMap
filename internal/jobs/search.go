package jobs

import (
	"context"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// SearchRequest binds an immutable search to an existing authenticated reader lease.
type SearchRequest struct {
	ContractVersion int                 `json:"contractVersion"`
	ProfileID       string              `json:"profileId"`
	ViewID          string              `json:"viewId"`
	Query           query.SearchRequest `json:"query"`
	Cursor          string              `json:"cursor,omitempty"`
}

// Validate rejects oversized continuation and selectors before acquiring a reader slot.
func (r SearchRequest) Validate() error {
	if r.ContractVersion != 1 || !identifier.MatchString(r.ProfileID) || !identifier.MatchString(r.ViewID) || !identifier.MatchString(r.Query.SnapshotID) || len(r.Cursor) > query.MaxSearchCursorBytes {
		return ErrInvalid
	}
	_, err := r.Query.Validate()
	return err
}

// ReadSearch shares the hierarchy concurrency and generation pin without renewing a view.
func (m *Manager) ReadSearch(ctx context.Context, owner string, in SearchRequest) (query.SearchPage, error) {
	var empty query.SearchPage
	if err := in.Validate(); err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	// The adapter carries identity only; search pagination never enters hierarchy validation.
	binding := HierarchyRequest{ContractVersion: 1, ProfileID: in.ProfileID, ViewID: in.ViewID, Query: query.Request{SnapshotID: in.Query.SnapshotID, RootID: in.Query.RootID, Metric: in.Query.Metric, Depth: 1, ElementBudget: 1}}
	release, err := m.reserveHierarchy(ctx, owner, binding)
	if err != nil {
		return empty, err
	}
	defer release()
	db, closeDB, err := m.generationDB(in.Query.SnapshotID)
	if err != nil {
		return empty, ErrViewExpired
	}
	defer closeDB()
	header, err := index.ReadGenerationHeader(ctx, db, in.Query.SnapshotID)
	if err != nil {
		return empty, err
	}
	if header.Profile.ID != in.ProfileID {
		return empty, query.ErrSnapshot
	}
	return query.Search(ctx, db, header, in.Query, in.Cursor)
}
