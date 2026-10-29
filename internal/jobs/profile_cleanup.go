package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// initializeProfileCleanup adds durable cleanup intent without changing generation or replay schemas.
func initializeProfileCleanup(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS profile_cleanup(profile_id TEXT NOT NULL,snapshot_id TEXT NOT NULL,job_id TEXT NOT NULL,binding_json TEXT NOT NULL DEFAULT '',attempted_count INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(profile_id,snapshot_id));`)
	return err
}

// scheduleProfileDeletion retires the identity and queues every recorded historical result atomically.
// Historical jobs and replay receipts remain evidence; no profile ID is redirected to another profile.
func (m *Manager) scheduleProfileDeletion(ctx context.Context, tx *sql.Tx, id string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id,outcome_json FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var job, raw string
		var out Outcome
		if err = rows.Scan(&job, &raw); err != nil {
			break
		}
		if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ID != job || out.Job.ProfileID != id {
			err = ErrUnavailable
			break
		}
		if out.Job.State != "completed" && out.Job.State != "failed" && out.Job.State != "cancelled" && out.Job.State != "interrupted" {
			err = ErrProfileActive
			break
		}
		if out.Job.ResultSnapshotID != nil {
			if !security.ValidID(*out.Job.ResultSnapshotID) {
				err = ErrUnavailable
				break
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, id, *out.Job.ResultSnapshotID, job); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if err = m.queueOrphanGenerations(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO profile_tombstones(id) VALUES(?)`, id); err != nil {
		return err
	}
	for _, statement := range []string{`DELETE FROM active_generations WHERE profile_id=?`, `DELETE FROM profile_sources WHERE profile_id=?`, `DELETE FROM profiles WHERE id=?`} {
		if _, err = tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return nil
}

// CleanupStatus reports current state separately from the immutable deletion receipt.
type CleanupStatus struct {
	ProfileID          string `json:"profileId"`
	Deleted            bool   `json:"deleted"`
	PendingGenerations int    `json:"pendingGenerations"`
	Complete           bool   `json:"complete"`
	Scope              string `json:"scope"`
}

// ProfileCleanupStatus retries recoverable own-data cleanup under the same manager serialization.
func (m *Manager) ProfileCleanupStatus(ctx context.Context, id string) (CleanupStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	out := CleanupStatus{ProfileID: id, Scope: "sealed-generations"}
	if !security.ValidID(id) {
		return out, ErrInvalid
	}
	if m.closed {
		return out, ErrUnavailable
	}
	var retired int
	if err := m.db.QueryRowContext(ctx, `SELECT count(*) FROM profile_tombstones WHERE id=?`, id).Scan(&retired); err != nil {
		return out, err
	}
	if retired != 1 {
		return out, ErrProfileMissing
	}
	m.cleanupProfiles(ctx)
	out.Deleted = true
	err := m.db.QueryRowContext(ctx, `SELECT count(*) FROM profile_cleanup WHERE profile_id=?`, id).Scan(&out.PendingGenerations)
	out.Complete = err == nil && out.PendingGenerations == 0
	return out, err
}

// cleanupProfiles processes a bounded batch; failures retain durable intent for status/startup retries.
// All callers hold m.mu (or execute during Open before publishing the manager).
func (m *Manager) cleanupProfiles(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	rows, err := m.db.QueryContext(ctx, `SELECT profile_id,snapshot_id,job_id,binding_json FROM profile_cleanup ORDER BY attempted_count,profile_id,snapshot_id LIMIT 200`)
	if err != nil {
		return
	}
	type work struct{ profile, snapshot, job, binding string }
	items := []work{}
	for rows.Next() {
		var w work
		if rows.Scan(&w.profile, &w.snapshot, &w.job, &w.binding) != nil {
			rows.Close()
			return
		}
		items = append(items, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	for _, w := range items {
		if ctx.Err() != nil {
			return
		}
		// Fair retries prevent one blocked batch from starving unrelated qualified generations.
		_, _ = m.db.ExecContext(ctx, `UPDATE profile_cleanup SET attempted_count=attempted_count+1 WHERE profile_id=? AND snapshot_id=?`, w.profile, w.snapshot)
		if m.cleanupGeneration(ctx, w.profile, w.snapshot, w.job, w.binding) == nil {
			_, _ = m.db.ExecContext(ctx, `DELETE FROM profile_cleanup WHERE profile_id=? AND snapshot_id=?`, w.profile, w.snapshot)
		}
	}
}

// cleanupGeneration only unlinks a qualified sealed index and its empty package-owned directory.
// A durable directory identity permits restart after unlink; unknown files and symlinks stay untouched.
func (m *Manager) cleanupGeneration(ctx context.Context, profile, snapshot, job, saved string) error {
	if !security.ValidID(profile) || !security.ValidID(snapshot) || !security.ValidID(job) {
		return ErrInvalid
	}
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	if m.generationReaders[snapshot] > 0 || m.leaseSnapshotProtected(snapshot) {
		return ErrConflict
	}
	parent, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations"))
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(snapshot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnavailable
	}
	dir, binding, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", snapshot))
	if err != nil {
		return err
	}
	defer dir.Close()
	if saved != "" {
		var previous scanner.RootBinding
		if json.Unmarshal([]byte(saved), &previous) != nil || previous != binding {
			return ErrUnavailable
		}
	}
	indexInfo, err := dir.Lstat("index.db")
	if err == nil {
		if !indexInfo.Mode().IsRegular() {
			return ErrUnavailable
		}
		// The cleanup owner already holds the reader lock; use the unleased descriptor opener.
		db, close, e := m.openGenerationDB(snapshot)
		if e != nil {
			return e
		}
		header, e := index.ReadGenerationHeader(ctx, db, snapshot)
		close()
		if e != nil || header.Profile.ID != profile || header.JobID != job {
			return ErrUnavailable
		}
		encoded, _ := json.Marshal(binding)
		if _, e = m.db.ExecContext(ctx, `UPDATE profile_cleanup SET binding_json=? WHERE profile_id=? AND snapshot_id=?`, string(encoded), profile, snapshot); e != nil {
			return e
		}
		current, e := dir.Lstat("index.db")
		if e != nil || !os.SameFile(indexInfo, current) {
			return ErrUnavailable
		}
		if e = dir.Remove("index.db"); e != nil {
			return e
		}
	} else if !errors.Is(err, os.ErrNotExist) || saved == "" {
		return ErrUnavailable
	}
	// Remove is intentionally non-recursive; unexpected files must never be interpreted as source data.
	current, err := parent.Lstat(snapshot)
	if err != nil || !os.SameFile(info, current) {
		return ErrUnavailable
	}
	if err = parent.Remove(snapshot); err != nil {
		return err
	}
	fd, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer fd.Close()
	return fd.Sync()
}

// queueOrphanGenerations covers a crash after directory publication but before job result persistence.
// Batches bound memory; sealed headers and existing job ownership both qualify each candidate.
func (m *Manager) queueOrphanGenerations(ctx context.Context, tx *sql.Tx, profile string) error {
	root, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	inspected := 0
	for {
		entries, e := directory.ReadDir(64)
		if e != nil && !errors.Is(e, io.EOF) {
			return e
		}
		for _, entry := range entries {
			inspected++
			if inspected > 4096 {
				return ErrActionBudget
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			snapshot := entry.Name()
			if !security.ValidID(snapshot) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			db, close, e := m.generationDB(snapshot)
			if e != nil {
				continue
			}
			h, e := index.ReadGenerationHeader(ctx, db, snapshot)
			close()
			if e != nil || h.Profile.ID != profile {
				continue
			}
			var owned int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE id=? AND json_extract(outcome_json,'$.job.profileId')=?`, h.JobID, profile).Scan(&owned); e != nil {
				return e
			}
			if owned != 1 {
				continue
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?) ON CONFLICT(profile_id,snapshot_id) DO NOTHING`, profile, snapshot, h.JobID); e != nil {
				return e
			}
		}
		if errors.Is(e, io.EOF) {
			return nil
		}
	}
}
