package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// recoverInterruptedJobs runs before dispatch. One transaction binds all repaired outcomes to their SQL identities.
// Accepted FIFO rows remain queued; recovery never synthesizes a replacement scan or reuses partial work.
func (m *Manager) recoverInterruptedJobs(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cursor int64
	first := true
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		query := `SELECT rowid,id,state,outcome_json FROM jobs WHERE state NOT IN ('completed','failed','cancelled','interrupted') AND id NOT IN (SELECT job_id FROM scan_queue)`
		args := []any{}
		if !first {
			query += ` AND rowid>?`
			args = append(args, cursor)
		}
		rows, e := tx.QueryContext(ctx, query+` ORDER BY rowid LIMIT 64`, args...)
		if e != nil {
			return e
		}
		type record struct {
			row            int64
			id, state, raw string
		}
		batch := make([]record, 0, 64)
		for rows.Next() {
			var r record
			if e = rows.Scan(&r.row, &r.id, &r.state, &r.raw); e != nil {
				break
			}
			batch = append(batch, r)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			var out Outcome
			if json.Unmarshal([]byte(r.raw), &out) != nil || out.Job.ID != r.id || out.Job.State != r.state || !identifier.MatchString(r.id) || !validPhaseTransition(r.state, "interrupted") || out.Job.ResultSnapshotID != nil || out.SnapshotState != "" {
				return ErrUnavailable
			}
			// Unbound work is preserved. This warning expresses possible residue, not a qualified deletion target.
			if r.state != "queued" && r.state != "preflight" {
				out.Job.Warnings = append(out.Job.Warnings, contracts.APIError{Code: "WORK_RECOVERY_REVIEW", MessageKey: "scan.work_may_remain", RequestID: r.id})
			}
			end := now()
			out.Job.State = "interrupted"
			out.Job.EndedAt = &end
			out.SnapshotEndedAt = nil
			raw, e := json.Marshal(out)
			if e != nil {
				return e
			}
			changed, e := tx.ExecContext(ctx, `UPDATE jobs SET state='interrupted',outcome_json=? WHERE id=? AND state=?`, string(raw), r.id, r.state)
			if e != nil {
				return e
			}
			count, e := changed.RowsAffected()
			if e != nil {
				return e
			}
			if count != 1 {
				return ErrUnavailable
			}
			if e = recordJobPhaseTx(ctx, tx, r.id, "interrupted", end); e != nil {
				return e
			}
			cursor = r.row
		}
		first = false
	}
	return tx.Commit()
}

// cancellationState distinguishes committed user cancellation from service shutdown.
// A service context ending cannot claim that the user cancelled the job, nor that it can safely resume.
func (m *Manager) cancellationState(jobID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runner != nil && m.runner.id == jobID && m.runner.desired == "cancel" {
		return "cancelled"
	}
	if m.ctx.Err() != nil {
		return "interrupted"
	}
	return "cancelled"
}
