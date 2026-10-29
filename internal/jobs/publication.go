package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrPublicationUncertain prevents contradictory terminal writes after an ambiguous commit.
var ErrPublicationUncertain = errors.New("PUBLICATION_OUTCOME_UNCERTAIN")

// publicationPoint exposes private deterministic test barriers without runtime configuration.
func (m *Manager) publicationPoint(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.publishHook != nil {
		return m.publishHook(phase)
	}
	return nil
}

// publishGeneration makes a durable sealed file visible before committing its sole public pointer.
// A failure after rename leaves an unreferenced owned generation, never a half-built default.
func (m *Manager) publishGeneration(ctx context.Context, o Outcome, staging, snapshot, state string) error {
	if state != "complete" && state != "partial" {
		return ErrInvalid
	}
	if !identifier.MatchString(snapshot) || staging != filepath.Join(m.state, "generations", snapshot)+".staging" {
		return ErrInvalid
	}
	generation := filepath.Join(m.state, "generations", snapshot)
	syncDir := func(path string) error {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		return errors.Join(f.Sync(), f.Close())
	}
	if err := m.publicationPoint(ctx, "before_staged_sync"); err != nil {
		return err
	}
	if err := syncDir(staging); err != nil {
		return err
	}
	if err := m.publicationPoint(ctx, "after_staged_sync"); err != nil {
		return err
	}
	if err := m.publicationPoint(ctx, "before_rename"); err != nil {
		return err
	}
	if err := os.Rename(staging, generation); err != nil {
		return err
	}
	if err := m.publicationPoint(ctx, "after_rename"); err != nil {
		return err
	}
	if err := m.publicationPoint(ctx, "before_parent_sync"); err != nil {
		return err
	}
	if err := syncDir(filepath.Dir(generation)); err != nil {
		return err
	}
	if err := m.publicationPoint(ctx, "after_parent_sync"); err != nil {
		return err
	}
	// Prepare derived query data while the new generation is still absent from public history.
	// Preparation obeys scan cancellation; exceeding the optional projection budget preserves the scan.
	if err := m.preparePublishedQueries(ctx, snapshot); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	// Once this checkpoint closes control admission, the atomic publish decision cannot be reversed.
	if err := m.jobCheckpoint(&o, true); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrUnavailable
	}
	if err := m.publicationPoint(ctx, "before_transaction"); err != nil {
		return err
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Completion time is distinct from the immutable collection interval.
	completedAt := now()
	o.Job.EndedAt = &completedAt
	o.Job.ResultSnapshotID = &snapshot
	o.SnapshotState = state
	o.Job.State = "completed"
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if err = m.publicationPoint(ctx, "before_job_write"); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?,outcome_json=? WHERE id=?`, o.Job.State, string(raw), o.Job.ID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrNotFound
	}
	if err = recordJobPhaseTx(ctx, tx, o.Job.ID, "completed", completedAt); err != nil {
		return err
	}
	if err = m.publicationPoint(ctx, "after_job_write"); err != nil {
		return err
	}
	if state == "complete" {
		if err = m.publicationPoint(ctx, "before_pointer_write"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO active_generations VALUES(?,?) ON CONFLICT(profile_id) DO UPDATE SET snapshot_id=excluded.snapshot_id`, o.Job.ProfileID, snapshot); err != nil {
			return err
		}
		if err = m.publicationPoint(ctx, "after_pointer_write"); err != nil {
			return err
		}
	}
	if err = m.publicationPoint(ctx, "before_commit"); err != nil {
		return err
	}
	// Cancellation is checked after the barrier and is also bound to the SQL transaction.
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return errors.Join(ErrPublicationUncertain, err)
	}
	// Attestation is best effort after commit and must never reverse a successful publication.
	if state == "complete" {
		attestCtx, attestCancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_ = m.attestPublishedGeneration(attestCtx, o.Job.ProfileID, snapshot, o.Job.ID)
		attestCancel()
	}
	// Release deduplication ownership atomically with the terminal database state.
	m.active = nil
	// A committed success cannot be turned into a contradictory failed job by a test observer.
	if m.publishHook != nil {
		_ = m.publishHook("after_commit")
	}
	return nil
}
