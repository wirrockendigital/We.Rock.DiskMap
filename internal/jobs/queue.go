package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// QueueCapacity bounds waiting work; the singleton worker owns one additional slot.
const QueueCapacity = 16

var ErrQueueFull = errors.New("QUEUE_FULL")

// initializeQueue stores acceptance order and immutable scope evidence in the control database.
func initializeQueue(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scan_queue(sequence INTEGER PRIMARY KEY AUTOINCREMENT,job_id TEXT NOT NULL UNIQUE,profile_id TEXT NOT NULL UNIQUE,sources_json TEXT NOT NULL)`)
	return err
}

// queueCount is used only while the manager lock protects acceptance and handoff.
func (m *Manager) queueCount() (int, error) {
	var n int
	err := m.db.QueryRow(`SELECT count(*) FROM scan_queue`).Scan(&n)
	return n, err
}

// pendingProfile returns the immutable accepted intent, including jobs still waiting.
func (m *Manager) pendingProfile(tx *sql.Tx, id string) (Outcome, bool, error) {
	if m.active != nil && m.active.Job.ProfileID == id {
		return *m.active, true, nil
	}
	var raw string
	err := tx.QueryRow(`SELECT j.outcome_json FROM scan_queue q JOIN jobs j ON j.id=q.job_id WHERE q.profile_id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, false, nil
	}
	if err != nil {
		return Outcome{}, false, err
	}
	var out Outcome
	if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ProfileID != id || out.Job.State != "queued" {
		return out, false, ErrUnavailable
	}
	return out, true, nil
}

// ensureWorker starts at most one dispatch loop, after acceptance has committed.
func (m *Manager) ensureWorker() {
	if m.workerRunning || m.closed || m.ctx.Err() != nil {
		return
	}
	m.workerRunning = true
	m.wg.Add(1)
	go m.runQueue()
}

// claimQueue binds dequeue and preflight entry atomically, without recapturing configuration.
func (m *Manager) claimQueue() (Outcome, profileSources, bool, error) {
	var out Outcome
	var sources profileSources
	tx, err := m.db.BeginTx(m.ctx, nil)
	if err != nil {
		return out, sources, false, err
	}
	defer tx.Rollback()
	var raw, sourceJSON, id string
	err = tx.QueryRow(`SELECT q.job_id,j.outcome_json,q.sources_json FROM scan_queue q JOIN jobs j ON j.id=q.job_id ORDER BY q.sequence LIMIT 1`).Scan(&id, &raw, &sourceJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return out, sources, false, nil
	}
	if err != nil {
		return out, sources, false, err
	}
	if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ID != id || out.Job.State != "queued" {
		return out, sources, false, ErrUnavailable
	}
	sources, err = loadSources(sourceJSON)
	if err != nil {
		return out, sources, false, err
	}
	started := now()
	out.Job.State = "preflight"
	out.Job.StartedAt = &started
	encoded, err := json.Marshal(out)
	if err != nil {
		return out, sources, false, err
	}
	if _, err = tx.Exec(`UPDATE jobs SET state='preflight',outcome_json=? WHERE id=?`, string(encoded), id); err != nil {
		return out, sources, false, err
	}
	if err = recordJobPhaseTx(m.ctx, tx, id, "preflight", started); err != nil {
		return out, sources, false, err
	}
	if _, err = tx.Exec(`DELETE FROM scan_queue WHERE job_id=?`, id); err != nil {
		return out, sources, false, err
	}
	if err = tx.Commit(); err != nil {
		return out, sources, false, errors.Join(ErrPublicationUncertain, err)
	}
	detached, err := detachOutcome(out)
	if err != nil {
		return out, sources, false, err
	}
	// Scans take priority over reconstructible visualization preparation.
	m.mapIndexMu.Lock()
	if m.mapIndexCancel != nil {
		m.mapIndexCancel()
	}
	m.mapIndexMu.Unlock()
	m.active = &detached
	jobctx, cancel := context.WithCancel(m.ctx)
	m.runner = &jobRunner{id: out.Job.ID, ctx: jobctx, cancel: cancel, desired: "run", wake: make(chan struct{})}
	return out, sources, true, nil
}

// runQueue hands the single scanner to the oldest committed intent after every terminal result.
func (m *Manager) runQueue() {
	defer m.wg.Done()
	for {
		m.mu.Lock()
		if m.closed || m.ctx.Err() != nil {
			m.workerRunning = false
			m.mu.Unlock()
			return
		}
		out, sources, found, err := m.claimQueue()
		if err != nil {
			m.closed = true
			m.cancel()
		}
		if err != nil || !found {
			m.workerRunning = false
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		m.execute(out, sources)
		m.mu.Lock()
		if m.runner != nil && m.runner.id == out.Job.ID {
			m.runner.cancel()
			m.runner = nil
		}
		m.mu.Unlock()
	}
}

// validateQueue prevents any restart dispatch until every bounded accepted record is qualified.
func (m *Manager) validateQueue() error {
	rows, err := m.db.Query(`SELECT q.job_id,q.profile_id,q.sources_json,j.state,j.outcome_json FROM scan_queue q LEFT JOIN jobs j ON j.id=q.job_id ORDER BY q.sequence`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		if n > QueueCapacity+1 {
			return ErrUnavailable
		}
		var id, profile, source, state, raw string
		if err = rows.Scan(&id, &profile, &source, &state, &raw); err != nil {
			return err
		}
		var out Outcome
		if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ID != id || out.Job.ProfileID != profile || state != "queued" || out.Job.State != "queued" || out.ScopeManifest == nil || !scanProfileSupported(out.Job.CapturedProfile) {
			return ErrUnavailable
		}
		if _, err = loadSources(source); err != nil {
			return err
		}
	}
	return rows.Err()
}
