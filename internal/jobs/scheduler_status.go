package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// RecordedSchedulerJob is historical manager evidence, not current DSM task or index state.
type RecordedSchedulerJob struct {
	JobID         string  `json:"jobId"`
	Trigger       string  `json:"trigger"`
	State         string  `json:"state"`
	CreatedAt     string  `json:"createdAt"`
	EndedAt       *string `json:"endedAt"`
	SnapshotID    *string `json:"snapshotId"`
	SnapshotState *string `json:"snapshotState"`
}

// SchedulerStatus separates successful local observations from unavailable calendar information.
type SchedulerStatus struct {
	StatusVersion int    `json:"statusVersion"`
	ProfileID     string `json:"profileId"`
	Schedule      struct {
		Capability string  `json:"capability"`
		Exists     *bool   `json:"exists"`
		Enabled    *bool   `json:"enabled"`
		NextRun    *string `json:"nextRun"`
	} `json:"schedule"`
	LastLocalCall      *RecordedSchedulerJob `json:"lastLocalCall"`
	LastSuccessfulScan *RecordedSchedulerJob `json:"lastSuccessfulScan"`
}

// SchedulerStatus reads the complete indexed history, never a truncated presentation window.
func (m *Manager) SchedulerStatus(parent context.Context, profile string) (SchedulerStatus, error) {
	out := SchedulerStatus{StatusVersion: 1, ProfileID: profile}
	out.Schedule.Capability = "unavailable"
	if !identifier.MatchString(profile) {
		return out, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 1500*time.Millisecond)
	defer cancel()
	var present int
	if e := m.db.QueryRowContext(ctx, "SELECT 1 FROM profiles WHERE id=?", profile).Scan(&present); errors.Is(e, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if e != nil {
		return out, e
	}
	var e error
	out.LastLocalCall, e = m.recordedSchedulerJob(ctx, profile, false)
	if e != nil {
		return out, e
	}
	out.LastSuccessfulScan, e = m.recordedSchedulerJob(ctx, profile, true)
	return out, e
}

// Predicates are fixed server-owned strings; the profile remains a bound SQL parameter.
func (m *Manager) recordedSchedulerJob(ctx context.Context, profile string, success bool) (*RecordedSchedulerJob, error) {
	predicate := `json_extract(outcome_json,'$.job.trigger') IN ('cli','scheduler')`
	if success {
		predicate = `state='completed' AND json_extract(outcome_json,'$.snapshotState')='complete'`
	}
	query := `SELECT id,json_extract(outcome_json,'$.job.id'),json_extract(outcome_json,'$.job.profileId'),state,json_extract(outcome_json,'$.job.state'),json_extract(outcome_json,'$.job.trigger'),json_extract(outcome_json,'$.job.createdAt'),json_extract(outcome_json,'$.job.endedAt'),json_extract(outcome_json,'$.job.resultSnapshotId'),json_extract(outcome_json,'$.snapshotState') FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=? AND ` + predicate + ` ORDER BY rowid DESC LIMIT 1`
	var r RecordedSchedulerJob
	var id, profileID, state string
	var ended, snapshot, quality sql.NullString
	e := m.db.QueryRowContext(ctx, query, profile).Scan(&r.JobID, &id, &profileID, &r.State, &state, &r.Trigger, &r.CreatedAt, &ended, &snapshot, &quality)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if !identifier.MatchString(id) || r.JobID != id || profileID != profile || r.State != state {
		return nil, ErrUnavailable
	}
	switch state {
	case "queued", "preflight", "scanning", "paused", "aggregating", "validating", "publishing", "completed", "failed", "cancelled", "interrupted":
	default:
		return nil, ErrUnavailable
	}
	if r.Trigger != "cli" && r.Trigger != "ui" && r.Trigger != "scheduler" {
		return nil, ErrUnavailable
	}
	if _, e = time.Parse(time.RFC3339Nano, r.CreatedAt); e != nil {
		return nil, ErrUnavailable
	}
	if ended.Valid {
		if _, e = time.Parse(time.RFC3339Nano, ended.String); e != nil {
			return nil, ErrUnavailable
		}
		r.EndedAt = &ended.String
	}
	if snapshot.Valid {
		if !identifier.MatchString(snapshot.String) {
			return nil, ErrUnavailable
		}
		r.SnapshotID = &snapshot.String
	}
	if quality.Valid && quality.String != "" {
		if quality.String != "complete" && quality.String != "partial" {
			return nil, ErrUnavailable
		}
		r.SnapshotState = &quality.String
	}
	if success && (r.SnapshotID == nil || r.EndedAt == nil || r.SnapshotState == nil || *r.SnapshotState != "complete") {
		return nil, ErrUnavailable
	}
	return &r, nil
}
