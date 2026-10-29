package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// StatusJob exposes committed identity and phase only, never captured source configuration.
type StatusJob struct {
	JobID           string  `json:"jobId"`
	ProfileID       string  `json:"profileId"`
	ProfileRevision int     `json:"profileRevision"`
	ScanMode        string  `json:"scanMode"`
	Trigger         string  `json:"trigger"`
	State           string  `json:"state"`
	AcceptedAt      string  `json:"acceptedAt"`
	StartedAt       *string `json:"startedAt"`
	QueuePosition   int     `json:"queuePosition"`
}

// StatusSnapshot is a single control-database observation, not a filesystem health claim.
type StatusSnapshot struct {
	ObservedAt string      `json:"observedAt"`
	Active     *StatusJob  `json:"active"`
	Queued     []StatusJob `json:"queued"`
}

const liveStatusPredicate = `state NOT IN ('completed','failed','cancelled','interrupted')`

// initializeStatusSnapshot adds a derived index without changing durable data or schema version.
func initializeStatusSnapshot(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS jobs_status_live ON jobs(state,id) WHERE `+liveStatusPredicate)
	return err
}

// validStatusJob rejects malformed provenance and impossible public phase combinations.
func validStatusJob(j StatusJob, queued bool) bool {
	if !security.ValidID(j.JobID) || !security.ValidID(j.ProfileID) || j.ProfileRevision < 1 || j.ProfileRevision > 2147483647 || !contracts.ValidScanMode(j.ScanMode) {
		return false
	}
	if j.Trigger != "ui" && j.Trigger != "cli" && j.Trigger != "scheduler" {
		return false
	}
	at, e := time.Parse(time.RFC3339Nano, j.AcceptedAt)
	if e != nil {
		return false
	}
	if queued {
		return j.State == "queued" && j.StartedAt == nil && j.QueuePosition >= 1 && j.QueuePosition <= QueueCapacity+1
	}
	if j.QueuePosition != 0 || j.StartedAt == nil {
		return false
	}
	started, e := time.Parse(time.RFC3339Nano, *j.StartedAt)
	if e != nil || started.Before(at) {
		return false
	}
	switch j.State {
	case "preflight", "scanning", "aggregating", "validating", "publishing", "paused":
		return true
	}
	return false
}

// ValidStatusSnapshot also guards private-service responses at the authenticated CGI boundary.
func ValidStatusSnapshot(v StatusSnapshot) bool {
	if _, e := time.Parse(time.RFC3339Nano, v.ObservedAt); e != nil {
		return false
	}
	if v.Queued == nil || len(v.Queued) > QueueCapacity+1 {
		return false
	}
	ids, profiles := map[string]bool{}, map[string]bool{}
	if v.Active != nil {
		if !validStatusJob(*v.Active, false) || len(v.Queued) > QueueCapacity {
			return false
		}
		ids[v.Active.JobID] = true
		profiles[v.Active.ProfileID] = true
	}
	for i, j := range v.Queued {
		if !validStatusJob(j, true) || j.QueuePosition != i+1 || ids[j.JobID] || profiles[j.ProfileID] {
			return false
		}
		ids[j.JobID] = true
		profiles[j.ProfileID] = true
	}
	raw, e := json.Marshal(v)
	return e == nil && len(raw) <= 65500
}

// statusText projects only small typed scalars; private manifests remain inside SQLite.
func statusText(path string, limit int) string {
	return fmt.Sprintf("CASE WHEN json_type(j.outcome_json,'%s')='text' AND length(CAST(json_extract(j.outcome_json,'%s') AS BLOB))<=%d THEN json_extract(j.outcome_json,'%s') END", path, path, limit, path)
}

// statusProjection preserves authoritative bindings without decoding the complete private outcome.
func statusProjection() string {
	return `json_object('jobId',` + statusText("$.job.id", 128) + `,
 'profileId',` + statusText("$.job.profileId", 128) + `,
 'state',` + statusText("$.job.state", 32) + `,
 'scanMode',` + statusText("$.job.scanMode", 32) + `,
 'trigger',` + statusText("$.job.trigger", 32) + `,
 'acceptedAt',` + statusText("$.job.createdAt", 64) + `,
 'startedAt',` + statusText("$.job.startedAt", 64) + `,
 'profileRevision',CASE WHEN json_type(j.outcome_json,'$.job.profileRevision')='integer' THEN json_extract(j.outcome_json,'$.job.profileRevision') END),
 CASE WHEN json_type(j.outcome_json,'$.job.endedAt')='null'
 AND json_type(j.outcome_json,'$.job.startedAt') IN ('null','text')
 AND (json_type(j.outcome_json,'$.job.startedAt')='null' OR length(CAST(json_extract(j.outcome_json,'$.job.startedAt') AS BLOB))<=64)
 AND json_type(j.outcome_json,'$.job.capturedProfile.id')='text'
 AND json_extract(j.outcome_json,'$.job.capturedProfile.id')=json_extract(j.outcome_json,'$.job.profileId')
 AND json_type(j.outcome_json,'$.job.capturedProfile.revision')='integer'
 AND json_extract(j.outcome_json,'$.job.capturedProfile.revision')=json_extract(j.outcome_json,'$.job.profileRevision')
 THEN 1 ELSE 0 END`
}

// Status reads bounded live rows and queue membership in one consistent transaction.
// The partial index excludes terminal history; no lifecycle mutex or source/index access is used.
func (m *Manager) Status(ctx context.Context) (StatusSnapshot, error) {
	ctx, cancel := m.observationContext(ctx)
	defer cancel()
	out, err := retryControlRead(ctx, func() (StatusSnapshot, error) { return m.observeStatus(ctx) })
	return out, observationError(err)
}

// observeStatus owns one complete read transaction; retries never reuse partial output.
func (m *Manager) observeStatus(ctx context.Context) (StatusSnapshot, error) {
	empty := StatusSnapshot{}
	ctx, cancel := m.observationContext(ctx)
	defer cancel()
	tx, e := m.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN length(CAST(j.id AS BLOB))<=128 THEN j.id END,CASE WHEN length(CAST(j.state AS BLOB))<=32 THEN j.state END,`+statusProjection()+`,q.sequence,CASE WHEN length(CAST(q.profile_id AS BLOB))<=128 THEN q.profile_id END FROM jobs j INDEXED BY jobs_status_live LEFT JOIN scan_queue q ON q.job_id=j.id WHERE `+liveStatusPredicate+` ORDER BY j.state,j.id LIMIT 18`)
	if e != nil {
		return empty, e
	}
	out := StatusSnapshot{ObservedAt: now(), Queued: []StatusJob{}}
	type waiting struct {
		seq int64
		j   StatusJob
	}
	pending := []waiting{}
	n := 0
	for rows.Next() {
		n++
		var id, state, raw string
		var sequence sql.NullInt64
		var profile sql.NullString
		var bound int
		if n > QueueCapacity+1 || rows.Scan(&id, &state, &raw, &bound, &sequence, &profile) != nil {
			rows.Close()
			return empty, ErrUnavailable
		}
		var j StatusJob
		if bound != 1 || len(raw) > 4096 || json.Unmarshal([]byte(raw), &j) != nil || j.JobID != id || j.State != state {
			rows.Close()
			return empty, ErrUnavailable
		}

		if state == "queued" {
			if !sequence.Valid || sequence.Int64 <= 0 || !profile.Valid || profile.String != j.ProfileID {
				rows.Close()
				return empty, ErrUnavailable
			}
			pending = append(pending, waiting{sequence.Int64, j})
		} else {
			if sequence.Valid || out.Active != nil {
				rows.Close()
				return empty, ErrUnavailable
			}
			out.Active = &j
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return empty, e
	}
	// Check bounded queue cardinality independently to detect orphan or terminal queue references.
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM scan_queue LIMIT 18)`).Scan(&count); e != nil {
		return empty, e
	}
	if count != len(pending) {
		return empty, ErrUnavailable
	}
	// At most seventeen rows make insertion sorting bounded and preserve signed sequence ordering.
	for i := 1; i < len(pending); i++ {
		for k := i; k > 0 && pending[k].seq < pending[k-1].seq; k-- {
			pending[k], pending[k-1] = pending[k-1], pending[k]
		}
	}
	for i, p := range pending {
		p.j.QueuePosition = i + 1
		out.Queued = append(out.Queued, p.j)
	}
	if !ValidStatusSnapshot(out) || ctx.Err() != nil {
		return empty, ErrUnavailable
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	return out, nil
}
