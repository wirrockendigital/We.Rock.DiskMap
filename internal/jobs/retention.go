package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// RetentionRequest changes only the number of qualified complete generations kept per profile.
type RetentionRequest struct {
	ContractVersion int `json:"contractVersion"`
	CompleteCount   int `json:"completeCount"`
}

func (r RetentionRequest) Validate() error {
	if r.ContractVersion != 1 {
		return ErrInvalid
	}
	switch r.CompleteCount {
	case 1, 2, 3, 7, 30:
		return nil
	}
	return ErrInvalid
}

// RetentionReceipt is immutable replay evidence; cleanup progress is read separately.
type RetentionReceipt struct {
	CompleteCount    int  `json:"completeCount"`
	CleanupScheduled bool `json:"cleanupScheduled"`
}

// initializeRetention stores the explicit default without rewriting other app settings.
func initializeRetention(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS retention_settings(singleton INTEGER PRIMARY KEY CHECK(singleton=1),complete_count INTEGER NOT NULL CHECK(complete_count IN (1,2,3,7,30))); INSERT OR IGNORE INTO retention_settings VALUES(1,2); CREATE TABLE IF NOT EXISTS retention_cleanup(profile_id TEXT NOT NULL,snapshot_id TEXT PRIMARY KEY,job_id TEXT NOT NULL,binding_json TEXT NOT NULL DEFAULT '',attempted_count INTEGER NOT NULL DEFAULT 0); CREATE TABLE IF NOT EXISTS retention_qualification(snapshot_id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,job_id TEXT NOT NULL,stamp TEXT NOT NULL,attempts INTEGER NOT NULL); CREATE TABLE IF NOT EXISTS retention_cursor(singleton INTEGER PRIMARY KEY CHECK(singleton=1),last_row INTEGER NOT NULL); INSERT OR IGNORE INTO retention_cursor VALUES(1,0);`)
	return err
}

// SetRetention commits settings and replay evidence together before attempting recoverable cleanup.
func (m *Manager) SetRetention(ctx context.Context, in RetentionRequest, principal, key string) (RetentionReceipt, error) {
	out := RetentionReceipt{}
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return out, ErrUnavailable
	}
	raw, _ := json.Marshal(in)
	r := ReplayRequest{principal, "storage.retention", key, string(raw)}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	previous, found, err := LookupReplay(tx, r)
	if err != nil {
		return out, err
	}
	if found {
		if json.Unmarshal(previous, &out) != nil {
			return out, ErrUnavailable
		}
		return out, nil
	}
	if _, err = tx.ExecContext(ctx, `UPDATE retention_settings SET complete_count=? WHERE singleton=1`, in.CompleteCount); err != nil {
		return out, err
	}
	out = RetentionReceipt{in.CompleteCount, true}
	encoded, _ := json.Marshal(out)
	if err = RecordReplay(tx, r, encoded); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	m.retainGenerations(ctx)
	return out, nil
}

type retainedGeneration struct {
	SnapshotID      string `json:"snapshotId"`
	JobID           string `json:"-"`
	EndedAt         string `json:"endedAt"`
	ProfileRevision int    `json:"profileRevision"`
	State           string `json:"state"`
	stamp           string
}

// qualifiedCompletes never counts missing, partial or corrupt results toward the retention promise.
func (m *Manager) qualifiedCompletes(ctx context.Context, profile string) ([]retainedGeneration, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT j.id,json_extract(j.outcome_json,'$.job.resultSnapshotId') FROM jobs j JOIN retention_qualification q ON q.job_id=j.id AND q.snapshot_id=json_extract(j.outcome_json,'$.job.resultSnapshotId') WHERE NOT EXISTS(SELECT 1 FROM integrity_generations g WHERE g.snapshot_id=q.snapshot_id AND g.quality != 'healthy') AND j.state='completed' AND q.stamp NOT IN ('removed','unsupported') AND json_extract(j.outcome_json,'$.job.profileId')=? AND json_extract(j.outcome_json,'$.snapshotState')='complete' ORDER BY j.rowid DESC LIMIT 4097`, profile)
	if err != nil {
		return nil, err
	}
	type candidate struct{ job, snapshot string }
	candidates := []candidate{}
	for rows.Next() {
		var c candidate
		var id sql.NullString
		if err = rows.Scan(&c.job, &id); err != nil {
			break
		}
		if id.Valid && identifier.MatchString(id.String) {
			c.snapshot = id.String
			candidates = append(candidates, c)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(candidates) > 4096 {
		return nil, ErrActionBudget
	}
	out := []retainedGeneration{}
	for _, o := range candidates {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		snapshot := o.snapshot
		var stamp string
		if m.db.QueryRowContext(ctx, `SELECT stamp FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=?`, snapshot, profile, o.job).Scan(&stamp) != nil || stamp == "" {
			continue
		}
		root, file, e := m.openRetentionFile(snapshot)
		if e != nil {
			continue
		}
		info, e := file.Stat()
		if e != nil || retainedStamp(info) != stamp {
			file.Close()
			root.Close()
			_, _ = m.db.ExecContext(ctx, `UPDATE retention_qualification SET stamp='' WHERE snapshot_id=?`, snapshot)
			continue
		}
		db, e := retainedFileDB(file)
		if e != nil {
			file.Close()
			root.Close()
			continue
		}
		h, e := index.ReadGenerationHeader(ctx, db, snapshot)
		db.Close()
		file.Close()
		root.Close()
		if e != nil || h.State != "complete" || h.Profile.ID != profile || h.JobID != o.job {
			continue
		}
		out = append(out, retainedGeneration{snapshot, h.JobID, h.EndedAt, h.Profile.Revision, "complete", stamp})
	}
	return out, nil
}

// retainGenerations runs only after publication/startup/settings, never to make a new scan pass preflight.
// The caller holds m.mu; bounded failures preserve all unqualified data for explicit diagnosis.
func (m *Manager) retainGenerations(ctx context.Context) {
	m.retainGenerationRound(ctx, false)
}

// Background rounds qualify one profile at a time; direct setting changes preserve their existing contract.
func (m *Manager) retainGenerationRound(ctx context.Context, incremental bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var keep int
	if m.db.QueryRowContext(ctx, `SELECT complete_count FROM retention_settings WHERE singleton=1`).Scan(&keep) != nil {
		return
	}
	rows, err := m.db.QueryContext(ctx, `SELECT profile_id,snapshot_id FROM active_generations ORDER BY profile_id LIMIT 1025`)
	if err != nil {
		return
	}
	type pointer struct{ profile, snapshot string }
	pointers := []pointer{}
	for rows.Next() {
		var p pointer
		if rows.Scan(&p.profile, &p.snapshot) != nil {
			rows.Close()
			return
		}
		pointers = append(pointers, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(pointers) > 1024 {
		return
	}

	if incremental && len(pointers) > 0 {
		chosen := pointers[0]
		for _, p := range pointers {
			if p.profile > m.retentionProfileCursor {
				chosen = p
				break
			}
		}
		m.retentionProfileCursor = chosen.profile
		pointers = []pointer{chosen}
	}
	for _, p := range pointers {
		if ctx.Err() != nil {
			return
		}
		// Immutable generation reads need no lifecycle ownership. No deletion decision survives a changed policy/pointer.
		probe := m.retentionProbe
		m.mu.Unlock()
		qualified, e := m.qualifiedCompletes(ctx, p.profile)
		if probe != nil {
			probe()
		}
		m.mu.Lock()
		if m.closed || ctx.Err() != nil {
			return
		}
		var currentKeep int
		var currentSnapshot string
		if m.db.QueryRowContext(ctx, `SELECT complete_count FROM retention_settings WHERE singleton=1`).Scan(&currentKeep) != nil ||
			m.db.QueryRowContext(ctx, `SELECT snapshot_id FROM active_generations WHERE profile_id=?`, p.profile).Scan(&currentSnapshot) != nil || currentKeep != keep || currentSnapshot != p.snapshot {
			continue
		}
		if e != nil {
			continue
		}
		// Integrity actions can invalidate a retained member without changing the active pointer.
		// Discard the whole old decision if any qualification changed while the lock was released.
		qualificationsCurrent := true
		for _, g := range qualified {
			var stamp string
			if m.generationIntegrityAllowed(g.SnapshotID) != nil ||
				m.db.QueryRowContext(ctx, `SELECT stamp FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=?`, g.SnapshotID, p.profile, g.JobID).Scan(&stamp) != nil || stamp != g.stamp {
				qualificationsCurrent = false
				break
			}
		}
		if !qualificationsCurrent {
			continue
		}
		activeValid := false
		protected := map[string]bool{p.snapshot: true}
		for i, g := range qualified {
			if g.SnapshotID == p.snapshot {
				activeValid = true
			}
			if i < keep {
				protected[g.SnapshotID] = true
			}
		}
		if !activeValid {
			continue
		}
		// Recheck the small protected set against its held-file stamp before deleting any older fallback.
		for _, g := range qualified {
			if !protected[g.SnapshotID] {
				continue
			}
			root, file, err := m.openRetentionFile(g.SnapshotID)
			if err != nil {
				activeValid = false
				break
			}
			info, err := file.Stat()
			file.Close()
			root.Close()
			if err != nil || retainedStamp(info) != g.stamp {
				activeValid = false
				break
			}
		}
		if !activeValid {
			continue
		}

		// A pending deletion is re-evaluated against the current policy before touching any filesystem object.
		for id := range protected {
			_, _ = m.db.ExecContext(ctx, `DELETE FROM retention_cleanup WHERE snapshot_id=?`, id)
		}
		for _, g := range qualified {
			if protected[g.SnapshotID] {
				continue
			}
			_, e = m.db.ExecContext(ctx, `INSERT OR IGNORE INTO retention_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, p.profile, g.SnapshotID, g.JobID)
			if e != nil {
				return
			}
		}
		pending, e := m.db.QueryContext(ctx, `SELECT snapshot_id,job_id,binding_json FROM retention_cleanup WHERE profile_id=? ORDER BY attempted_count,rowid LIMIT 200`, p.profile)
		if e != nil {
			continue
		}
		type item struct{ snapshot, job, binding string }
		items := []item{}
		for pending.Next() {
			var w item
			if pending.Scan(&w.snapshot, &w.job, &w.binding) != nil {
				break
			}
			items = append(items, w)
		}
		e = pending.Err()
		pending.Close()
		if e != nil {
			continue
		}
		for _, w := range items {
			if ctx.Err() != nil {
				return
			}
			if protected[w.snapshot] {
				continue
			}
			// Persist fairness before attempting a blocked object so later candidates cannot starve.
			if _, e = m.db.ExecContext(ctx, `UPDATE retention_cleanup SET attempted_count=attempted_count+1 WHERE snapshot_id=?`, w.snapshot); e != nil {
				return
			}
			if m.removeRetainedGeneration(ctx, p.profile, w.snapshot, w.job, w.binding) == nil {
				_, _ = m.db.ExecContext(ctx, `DELETE FROM retention_cleanup WHERE snapshot_id=?`, w.snapshot)
				_, _ = m.db.ExecContext(ctx, `UPDATE retention_qualification SET stamp='removed' WHERE snapshot_id=?`, w.snapshot)
			}
		}
	}
}
