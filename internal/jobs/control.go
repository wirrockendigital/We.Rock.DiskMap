package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// JobControlStatus separates a committed request from the worker's actual paused state.
type JobControlStatus struct {
	Requested string `json:"requested"`
	Version   int64  `json:"version"`
}

// JobControlReceipt is an immutable acknowledgement, not a claim that a syscall has returned.
type JobControlReceipt struct {
	JobID   string `json:"jobId"`
	Action  string `json:"action"`
	Version int64  `json:"version"`
}

// jobRunner exists only in one service lifetime; interrupted work cannot be resumed.
type jobRunner struct {
	// Committed control intent avoids lifecycle-lock contention on ordinary metadata checkpoints.
	controlPending atomic.Bool
	id             string
	ctx            context.Context
	cancel         context.CancelFunc
	desired        string
	wake           chan struct{}
	closing        bool
}

// initializeJobControls adds bounded current intent without rewriting historical job outcomes.
func initializeJobControls(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS job_controls(job_id TEXT PRIMARY KEY,requested TEXT NOT NULL,version INTEGER NOT NULL)`)
	return err
}

// readJobControl returns only the current intent, never accumulated polling or control history.
func (m *Manager) readJobControl(ctx context.Context, id string) (*JobControlStatus, error) {
	var out JobControlStatus
	err := m.db.QueryRowContext(ctx, `SELECT requested,version FROM job_controls WHERE job_id=?`, id).Scan(&out.Requested, &out.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ControlJob binds guarded mutations and replay in the same domain transaction.
func (m *Manager) ControlJob(ctx context.Context, in security.JobActionRequest, principal, key string) (JobControlReceipt, error) {
	var out JobControlReceipt
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{Principal: principal, Key: key, Operation: "job." + in.Action, Payload: string(raw)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return out, ErrUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	saved, found, err := LookupReplay(tx, replay)
	if err != nil {
		return out, err
	}
	if found {
		if json.Unmarshal(saved, &out) != nil {
			return out, ErrUnavailable
		}
		return out, nil
	}
	var encoded, state string
	if err = tx.QueryRowContext(ctx, `SELECT state,outcome_json FROM jobs WHERE id=?`, in.JobID).Scan(&state, &encoded); errors.Is(err, sql.ErrNoRows) {
		return out, ErrInvalid
	} else if err != nil {
		return out, err
	}
	var job Outcome
	if json.Unmarshal([]byte(encoded), &job) != nil || job.Job.ID != in.JobID {
		return out, ErrUnavailable
	}
	if terminalJob(state) {
		return out, ErrConflict
	}
	runner := m.runner
	queued := state == "queued"
	// Queue removal cannot cancel a job that started after the client last polled.
	if in.Action == "cancel-queued" && !queued {
		return out, ErrConflict
	}
	if queued && in.Action != "cancel" && in.Action != "cancel-queued" {
		return out, ErrConflict
	}
	if !queued && (runner == nil || runner.id != in.JobID || runner.closing) {
		return out, ErrConflict
	}
	if !queued && runner.desired == "cancel" {
		return out, ErrConflict
	}
	if !queued && in.Action == "resume" && runner.desired != "pause" && state != "paused" {
		return out, ErrConflict
	}
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT version FROM job_controls WHERE job_id=?`, in.JobID).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if version >= 9007199254740991 {
		return out, ErrConflict
	}
	version++
	desired := in.Action
	if desired == "cancel-queued" {
		desired = "cancel"
	}
	if desired == "resume" {
		desired = "run"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_controls VALUES(?,?,?) ON CONFLICT(job_id) DO UPDATE SET requested=excluded.requested,version=excluded.version`, in.JobID, desired, version); err != nil {
		return out, err
	}
	if queued {
		job.Job.State = "cancelled"
		end := now()
		job.Job.EndedAt = &end
		body, _ := json.Marshal(job)
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',outcome_json=? WHERE id=?`, string(body), in.JobID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM scan_queue WHERE job_id=?`, in.JobID); err != nil {
			return out, err
		}
		if err = recordJobPhaseTx(ctx, tx, in.JobID, "cancelled", end); err != nil {
			return out, err
		}
	}
	out = JobControlReceipt{JobID: in.JobID, Action: in.Action, Version: version}
	result, _ := json.Marshal(out)
	if err = RecordReplay(tx, replay, result); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		m.closed = true
		m.cancel()
		return out, ErrPublicationUncertain
	}
	if !queued {
		runner.desired = desired
		runner.controlPending.Store(desired != "run")
		close(runner.wake)
		runner.wake = make(chan struct{})
		if desired == "cancel" {
			runner.cancel()
		}
	}
	return out, nil
}

// runningCheckpoint keeps the normal scan path independent of unrelated maintenance locks.
// Pause acknowledgement and the final publication boundary still use the serialized control path.
func (m *Manager) runningCheckpoint(o *Outcome, runner *jobRunner) error {
	if runner == nil || runner.id != o.Job.ID {
		return ErrUnavailable
	}
	if err := runner.ctx.Err(); err != nil {
		return err
	}
	if !runner.controlPending.Load() {
		return nil
	}
	return m.jobCheckpoint(o, false)
}

// jobCheckpoint acknowledges pause only after prior metadata work has returned.
// It never holds the manager lock while waiting for resume or cancellation.
func (m *Manager) jobCheckpoint(o *Outcome, closing bool) error {
	for {
		m.mu.Lock()
		runner := m.runner
		if runner == nil || runner.id != o.Job.ID {
			m.mu.Unlock()
			if err := m.ctx.Err(); err != nil {
				return err
			}
			return ErrUnavailable
		}
		if err := runner.ctx.Err(); err != nil {
			m.mu.Unlock()
			return err
		}
		if runner.desired != "pause" {
			if closing {
				runner.closing = true
			}
			m.mu.Unlock()
			return nil
		}
		phase := o.Job.State
		o.Job.State = "paused"
		if err := m.storePauseState(*o); err != nil {
			m.mu.Unlock()
			return errors.Join(ErrPublicationUncertain, err)
		}
		wake := runner.wake
		ctx := runner.ctx
		m.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
		}
		m.mu.Lock()
		o.Job.State = phase
		err := m.storePauseState(*o)
		m.mu.Unlock()
		if err != nil {
			return errors.Join(ErrPublicationUncertain, err)
		}
	}
}

// storePauseState changes current presentation only; finite phase history retains the real phase.
func (m *Manager) storePauseState(o Outcome) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if _, err = m.db.Exec(`UPDATE jobs SET state=?,outcome_json=? WHERE id=?`, o.Job.State, string(raw), o.Job.ID); err != nil {
		return err
	}
	detached, err := detachOutcome(o)
	if err == nil {
		m.active = &detached
	}
	return err
}
