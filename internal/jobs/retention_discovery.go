package jobs

import (
	"context"
	"database/sql"
	"time"
)

// discoverRetention advances at most 64 raw rows. The runtime cursor wraps so
// previously nonterminal rows remain discoverable without idle durable writes.
func (m *Manager) discoverRetention(ctx context.Context) error {
	if !m.retentionDiscoveryReady {
		if err := m.readDB.QueryRowContext(ctx, `SELECT last_row FROM retention_cursor WHERE singleton=1`).Scan(&m.retentionDiscoveryCursor); err != nil {
			return err
		}
		m.retentionDiscoveryReady = true
	}
	rows, err := m.readDB.QueryContext(ctx, `SELECT j.rowid,j.id,json_extract(j.outcome_json,'$.job.profileId'),json_extract(j.outcome_json,'$.job.resultSnapshotId'),j.state,json_extract(j.outcome_json,'$.snapshotState'),q.snapshot_id FROM jobs j LEFT JOIN retention_qualification q ON q.snapshot_id=json_extract(j.outcome_json,'$.job.resultSnapshotId') WHERE j.rowid>? ORDER BY j.rowid LIMIT 64`, m.retentionDiscoveryCursor)
	if err != nil {
		return err
	}
	type candidate struct{ snapshot, profile, job string }
	batch := []candidate{}
	next := int64(0)
	for rows.Next() {
		var row int64
		var job, profile, state string
		var snapshot, quality, known sql.NullString
		if err = rows.Scan(&row, &job, &profile, &snapshot, &state, &quality, &known); err != nil {
			break
		}
		next = row
		if state == "completed" && quality.String == "complete" && identifier.MatchString(snapshot.String) && !known.Valid {
			batch = append(batch, candidate{snapshot.String, profile, job})
		}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(batch) > 0 {
		tx, e := m.db.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, r := range batch {
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO retention_qualification(snapshot_id,profile_id,job_id,stamp,attempts) VALUES(?,?,?,'',0)`, r.snapshot, r.profile, r.job); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE retention_cursor SET last_row=? WHERE singleton=1`, next); e != nil {
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	m.retentionDiscoveryCursor = next
	return nil
}

// retentionCandidate examines one more row than the bounded delay map can hold.
// Persisted attempt ordering therefore cannot hide all eligible rows behind delayed ones.
func (m *Manager) retentionCandidate(ctx context.Context, now time.Time) (string, string, string, int64, error) {
	rows, err := m.readDB.QueryContext(ctx, `SELECT snapshot_id,profile_id,job_id,attempts FROM retention_qualification WHERE stamp='' AND NOT EXISTS(SELECT 1 FROM integrity_generations g WHERE g.snapshot_id=retention_qualification.snapshot_id AND g.quality!='healthy') ORDER BY attempts,rowid LIMIT 129`)
	if err != nil {
		return "", "", "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var snapshot, profile, job string
		var attempts int64
		if err = rows.Scan(&snapshot, &profile, &job, &attempts); err != nil {
			return "", "", "", 0, err
		}
		if until, ok := m.retentionRetry[snapshot]; !ok || !now.Before(until) {
			return snapshot, profile, job, attempts, nil
		}
	}
	return "", "", "", 0, rows.Err()
}

// deferRetentionCandidate stores only scheduling hints, never qualification truth.
// Evicting the oldest due hint bounds memory; persisted attempts maintain fairness.
func (m *Manager) deferRetentionCandidate(snapshot string, attempts int64, now time.Time) {
	if m.retentionRetry == nil {
		m.retentionRetry = map[string]time.Time{}
	}
	if _, ok := m.retentionRetry[snapshot]; !ok && len(m.retentionRetry) >= 128 {
		oldest := ""
		var due time.Time
		for id, t := range m.retentionRetry {
			if oldest == "" || t.Before(due) {
				oldest = id
				due = t
			}
		}
		delete(m.retentionRetry, oldest)
	}
	delay := time.Second
	for i := int64(0); i < attempts && delay < 60*time.Second; i++ {
		delay *= 2
	}
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	m.retentionRetry[snapshot] = now.Add(delay)
}
