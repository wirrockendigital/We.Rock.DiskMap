package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// JobPhase records real phase entry, never a timer-derived percentage.
type JobPhase struct {
	State     string `json:"state"`
	EnteredAt string `json:"enteredAt"`
}

// StoredDataStatus is independent of the queried job's execution and result quality.
type StoredDataStatus struct {
	SnapshotID *string          `json:"snapshotId"`
	Quality    string           `json:"quality"`
	Freshness  string           `json:"freshness"`
	EndedAt    *string          `json:"endedAt"`
	AgeSeconds *contracts.Bytes `json:"ageSeconds"`
}

// terminalJob distinguishes the finite execution phases from completed outcomes.
func terminalJob(state string) bool {
	return state == "completed" || state == "failed" || state == "cancelled" || state == "interrupted"
}

// validPhaseTransition rejects skipping validation or publishing a premature completion.
func validPhaseTransition(from, to string) bool {
	if terminalJob(from) {
		return false
	}
	if to == "failed" || to == "cancelled" || to == "interrupted" {
		return from == "queued" || from == "preflight" || from == "scanning" || from == "aggregating" || from == "validating" || from == "publishing" || from == "paused"
	}
	next := map[string]string{"queued": "preflight", "preflight": "scanning", "scanning": "validating", "validating": "publishing", "publishing": "completed"}
	return next[from] == to && to != ""
}

// initializeJobPhases adds history without rewriting existing accepted or completed jobs.
func initializeJobPhases(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS job_phases(job_id TEXT NOT NULL,sequence INTEGER NOT NULL CHECK(sequence BETWEEN 1 AND 8),state TEXT NOT NULL,entered_at TEXT NOT NULL,PRIMARY KEY(job_id,sequence))`)
	return err
}

// recordJobPhaseTx joins each state change to the same transaction as its durable job record.
func recordJobPhaseTx(ctx context.Context, tx *sql.Tx, jobID, state, at string) error {
	if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
		return ErrInvalid
	}
	var previous string
	var sequence int
	err := tx.QueryRowContext(ctx, `SELECT sequence,state FROM job_phases WHERE job_id=? ORDER BY sequence DESC LIMIT 1`, jobID).Scan(&sequence, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		// Previously accepted jobs have no phase rows; recovery records their observed interruption.
		if state != "queued" && state != "interrupted" && state != "cancelled" && state != "failed" {
			return ErrInvalid
		}
	} else if err != nil {
		return err
	} else if !validPhaseTransition(previous, state) {
		return ErrInvalid
	}
	if sequence >= 8 {
		return ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_phases VALUES(?,?,?,?)`, jobID, sequence+1, state, at)
	return err
}

// persistOutcome atomically stores a new phase and its complete captured-job record.
func (m *Manager) persistOutcome(o Outcome) error {
	ctx := context.Background()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,outcome_json=? WHERE id=?`, o.Job.State, string(raw), o.Job.ID)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrInvalid
	}
	if err = recordJobPhaseTx(ctx, tx, o.Job.ID, o.Job.State, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// phase persists phase entry before starting that phase's work; private hooks only support tests.
func (m *Manager) phase(ctx context.Context, o *Outcome, next string) error {
	if err := scanner.Checkpoint(ctx); err != nil {
		return err
	}
	if !validPhaseTransition(o.Job.State, next) {
		return ErrInvalid
	}
	m.mu.Lock()
	o.Job.State = next
	if next == "preflight" {
		started := now()
		o.Job.StartedAt = &started
	}
	err := m.persistOutcome(*o)
	if err == nil {
		var detached Outcome
		detached, err = detachOutcome(*o)
		if err == nil {
			m.active = &detached
		}
	}
	m.mu.Unlock()
	if err != nil {
		return errors.Join(ErrPublicationUncertain, err)
	}
	if m.phaseHook != nil {
		if err := m.phaseHook(next); err != nil {
			return err
		}
	}
	return scanner.Checkpoint(ctx)
}

// phaseHistory reads a fixed maximum, without appending events for polling requests.
func (m *Manager) phaseHistory(ctx context.Context, id string) ([]JobPhase, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT state,entered_at FROM job_phases WHERE job_id=? ORDER BY sequence LIMIT 8`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobPhase{}
	for rows.Next() {
		var p JobPhase
		if err = rows.Scan(&p.State, &p.EnteredAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
