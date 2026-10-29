package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// ErrScanResultUnavailable distinguishes pending/failed jobs from qualified sealed results.
var ErrScanResultUnavailable = errors.New("SCAN_RESULT_UNAVAILABLE")

// ScanBoundaryReport belongs to the named job result, independently of the active full snapshot.
type ScanBoundaryReport struct {
	JobID           string                   `json:"jobId"`
	SnapshotID      string                   `json:"snapshotId"`
	State           string                   `json:"state"`
	EndedAt         string                   `json:"endedAt"`
	ProfileRevision int                      `json:"profileRevision"`
	Boundaries      []contracts.ScanBoundary `json:"boundaries"`
	Count           contracts.Bytes          `json:"count"`
	Truncated       bool                     `json:"truncated"`
}

// ScanBoundaries accepts stable IDs only and verifies job, profile and immutable header membership.
func (m *Manager) ScanBoundaries(ctx context.Context, profileID, jobID string) (ScanBoundaryReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out ScanBoundaryReport
	if m.closed {
		return out, ErrUnavailable
	}
	if !identifier.MatchString(profileID) || !identifier.MatchString(jobID) {
		return out, ErrInvalid
	}
	var raw string
	err := m.db.QueryRowContext(ctx, `SELECT j.outcome_json FROM jobs j JOIN profiles p ON p.id=? WHERE j.id=? AND json_extract(j.outcome_json,'$.job.profileId')=p.id`, profileID, jobID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	var job Outcome
	if json.Unmarshal([]byte(raw), &job) != nil || job.Job.ID != jobID || job.Job.ProfileID != profileID {
		return out, ErrUnavailable
	}
	if job.Job.State != "completed" || job.Job.ResultSnapshotID == nil || job.SnapshotEndedAt == nil || (job.SnapshotState != "complete" && job.SnapshotState != "partial") {
		return out, ErrScanResultUnavailable
	}
	db, close, err := m.generationDB(*job.Job.ResultSnapshotID)
	if err != nil {
		return out, ErrScanResultUnavailable
	}
	defer close()
	h, err := index.ReadGenerationHeader(ctx, db, *job.Job.ResultSnapshotID)
	if err != nil || h.JobID != jobID || h.Profile.ID != profileID || h.Profile.Revision != job.Job.ProfileRevision || h.State != job.SnapshotState || h.EndedAt != *job.SnapshotEndedAt {
		return out, ErrScanResultUnavailable
	}
	examples, err := boundaryExamples(ctx, db)
	if err != nil {
		return out, err
	}
	out = ScanBoundaryReport{JobID: jobID, SnapshotID: h.SnapshotID, State: h.State, EndedAt: h.EndedAt, ProfileRevision: h.Profile.Revision, Boundaries: examples, Count: h.ScanBoundaryCount, Truncated: string(h.ScanBoundaryCount) != strconv.Itoa(len(examples))}
	return out, nil
}
