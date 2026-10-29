package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"strconv"
	"sync"
	"time"
)

// ProgressEstimate is a labelled historical observation, never an exact denominator.
type ProgressEstimate struct {
	Entries    string `json:"entries"`
	SnapshotID string `json:"snapshotId"`
	Basis      string `json:"basis"`
}

// EffectiveResourcePolicy persists the selected preset with explicit duration units.
// Missing historical records remain unknown rather than inheriting current defaults.
type EffectiveResourcePolicy struct {
	Version        int    `json:"version"`
	Mode           string `json:"mode"`
	Workers        int    `json:"workers"`
	DirectoryBatch int    `json:"directoryBatch"`
	PaceEvery      int    `json:"paceEvery"`
	PaceDelayMs    int64  `json:"paceDelayMs"`
	SQLiteCacheKiB int    `json:"sqliteCacheKiB"`
	TempCacheKiB   int    `json:"tempCacheKiB"`
}

// effectivePolicy copies the immutable worker preset into its persisted presentation form.
func effectivePolicy(p resources.Policy) *EffectiveResourcePolicy {
	return &EffectiveResourcePolicy{p.Version, p.Mode, p.Workers, p.DirectoryBatch, p.PaceEvery, p.PaceDelay.Milliseconds(), p.SQLiteCacheKiB, p.TempCacheKiB}
}

// valid accepts only a complete recorded preset, never a partial or invented claim.
func (p *EffectiveResourcePolicy) valid() bool {
	if p == nil {
		return false
	}
	expected, err := resources.Resolve(p.Mode, contracts.ResourceLimits{Workers: p.Workers})
	return err == nil && *p == *effectivePolicy(expected)
}

// JobProgress separates measured metadata, lifecycle and unknown total work.
type JobProgress struct {
	ResourcePolicy    *EffectiveResourcePolicy `json:"resourcePolicy"`
	CreatedAt         string                   `json:"createdAt"`
	StartedAt         *string                  `json:"startedAt"`
	EndedAt           *string                  `json:"endedAt"`
	ErrorCodes        []string                 `json:"errorCodes"`
	JobID             string                   `json:"jobId"`
	ProfileID         string                   `json:"profileId"`
	State             string                   `json:"state"`
	Trigger           string                   `json:"trigger"`
	Mode              string                   `json:"mode"`
	Entries           string                   `json:"entries"`
	Files             string                   `json:"files"`
	Directories       string                   `json:"directories"`
	Specials          string                   `json:"specials"`
	UnknownLogical    string                   `json:"unknownLogical"`
	KnownLogicalBytes string                   `json:"knownLogicalBytes"`
	Warnings          string                   `json:"warnings"`
	ObservedAt        *string                  `json:"observedAt"`
	ElapsedSeconds    *string                  `json:"elapsedSeconds"`
	ExactTotal        *string                  `json:"exactTotal"`
	Estimate          *ProgressEstimate        `json:"estimate"`
	TargetID          string                   `json:"targetId"`
	Measured          bool                     `json:"measured"`
	PublishedComplete bool                     `json:"publishedComplete"`
}
type progressRecord struct {
	ResourcePolicy *EffectiveResourcePolicy `json:"resourcePolicy"`
	Observed       bool                     `json:"observed"`
	Sample         scanner.Progress         `json:"sample"`
	At             string                   `json:"at"`
	Estimate       *ProgressEstimate        `json:"estimate"`
}

// initializeProgress adds bounded presentation detail without changing provenance or replay records.
func initializeProgress(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS job_progress(job_id TEXT PRIMARY KEY, detail_json TEXT NOT NULL); CREATE INDEX IF NOT EXISTS jobs_history_profile ON jobs(json_extract(outcome_json,'$.job.profileId'))`)
	return err
}

// trackProgress persists coalesced observations outside the scanner; completion joins its one writer.
func (m *Manager) trackProgress(o Outcome) (func(scanner.Progress), func()) {
	record := progressRecord{Sample: scanner.Progress{KnownLogicalBytes: "0"}}
	// Persist the selected policy at execution start; later profile edits cannot rewrite it.
	if policy, err := resources.Resolve(o.Job.CapturedProfile.ResourceMode, o.Job.CapturedProfile.Limits); err == nil {
		record.ResourcePolicy = effectivePolicy(policy)
	}
	var previous string
	estimateCtx, estimateCancel := context.WithTimeout(m.ctx, time.Second)
	_ = m.db.QueryRowContext(estimateCtx, `SELECT json_object('job',json_object('profileRevision',json_extract(j.outcome_json,'$.job.profileRevision'),'resultSnapshotId',CASE WHEN length(CAST(json_extract(j.outcome_json,'$.job.resultSnapshotId') AS BLOB))<=128 THEN json_extract(j.outcome_json,'$.job.resultSnapshotId') END,'entriesVisited',CASE WHEN length(CAST(json_extract(j.outcome_json,'$.job.entriesVisited') AS BLOB))<=20 THEN json_extract(j.outcome_json,'$.job.entriesVisited') END),'snapshotState',json_extract(j.outcome_json,'$.snapshotState')) FROM active_generations a JOIN jobs j ON json_extract(j.outcome_json,'$.job.resultSnapshotId')=a.snapshot_id WHERE a.profile_id=? AND j.state='completed' LIMIT 1`, o.Job.ProfileID).Scan(&previous)
	estimateCancel()
	var old Outcome
	if json.Unmarshal([]byte(previous), &old) == nil && old.SnapshotState == "complete" && old.Job.ResultSnapshotID != nil && old.Job.ProfileRevision == o.Job.ProfileRevision && len(old.Job.EntriesVisited) <= 20 && identifier.MatchString(*old.Job.ResultSnapshotID) {
		record.Estimate = &ProgressEstimate{old.Job.EntriesVisited, *old.Job.ResultSnapshotID, "previous-complete"}
	}
	save := func(current progressRecord) {
		raw, _ := json.Marshal(current)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		// A failed sample remains unavailable rather than manufacturing successful progress.
		_, _ = m.db.ExecContext(ctx, `INSERT INTO job_progress(job_id,detail_json) VALUES(?,?) ON CONFLICT(job_id) DO UPDATE SET detail_json=excluded.detail_json`, o.Job.ID, string(raw))
	}
	observe, join := coalescedProgress(record, time.Second, save)
	var finishOnce sync.Once
	return observe, func() {
		finishOnce.Do(func() {
			join()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = m.db.ExecContext(ctx, `DELETE FROM job_progress WHERE job_id IN (SELECT p.job_id FROM job_progress p JOIN jobs j ON j.id=p.job_id WHERE j.state IN ('completed','cancelled','failed','interrupted') ORDER BY j.rowid DESC LIMIT -1 OFFSET 1000)`)
		})
	}
}

// coalescedProgress owns one latest-value slot and one writer goroutine per job.
// The scanner never waits on the sink; each cadence starts after the preceding write completes.
func coalescedProgress(initial progressRecord, cadence time.Duration, save func(progressRecord)) (func(scanner.Progress), func()) {
	var mu sync.Mutex
	latest, dirty, closed := initial, true, false
	wake, stop, done := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
	take := func() (progressRecord, bool) {
		mu.Lock()
		defer mu.Unlock()
		current, pending := latest, dirty
		dirty = false
		return current, pending
	}
	go func() {
		defer close(done)
		for {
			if current, pending := take(); pending {
				save(current)
			}
			timer := time.NewTimer(cadence)
			select {
			case <-stop:
				timer.Stop()
				// Always attempt the final value, even if its previous periodic write failed.
				current, _ := take()
				save(current)
				return
			case <-timer.C:
			}
			select {
			case <-stop:
				current, _ := take()
				save(current)
				return
			case <-wake:
			}
		}
	}()
	observe := func(p scanner.Progress) {
		mu.Lock()
		if closed {
			mu.Unlock()
			return
		}
		latest.Sample, latest.Observed, latest.At, dirty = p, true, now(), true
		mu.Unlock()
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	var once sync.Once
	finish := func() {
		once.Do(func() { mu.Lock(); closed = true; mu.Unlock(); close(stop) })
		<-done
	}
	return observe, finish
}

// progressProjection returns only bounded public fields from possibly large private outcomes.
func progressProjection() string {
	text := func(path string, n int) string { return statusText(path, n) }
	return `CASE WHEN json_valid(j.outcome_json)
 AND json_extract(j.outcome_json,'$.job.capturedProfile.id')=json_extract(j.outcome_json,'$.job.profileId')
 AND json_type(j.outcome_json,'$.job.profileRevision')='integer'
 AND json_extract(j.outcome_json,'$.job.profileRevision')>0
 AND json_extract(j.outcome_json,'$.job.capturedProfile.revision')=json_extract(j.outcome_json,'$.job.profileRevision')
 AND json_type(j.outcome_json,'$.job.startedAt') IN ('null','text')
 AND json_type(j.outcome_json,'$.job.endedAt') IN ('null','text')
 AND (json_type(j.outcome_json,'$.job.startedAt')='null' OR length(CAST(json_extract(j.outcome_json,'$.job.startedAt') AS BLOB))<=40)
 AND (json_type(j.outcome_json,'$.job.endedAt')='null' OR length(CAST(json_extract(j.outcome_json,'$.job.endedAt') AS BLOB))<=40)
 THEN json_object('job',json_object('id',` + text("$.job.id", 128) + `,
 'profileId',` + text("$.job.profileId", 128) + `,'state',` + text("$.job.state", 32) + `,
 'trigger',` + text("$.job.trigger", 32) + `,'scanMode',` + text("$.job.scanMode", 32) + `,
 'createdAt',` + text("$.job.createdAt", 40) + `,'startedAt',` + text("$.job.startedAt", 40) + `,
 'endedAt',` + text("$.job.endedAt", 40) + `,'resultSnapshotId',` + text("$.job.resultSnapshotId", 128) + `,
 'capturedProfile',json_object('resourceMode',` + text("$.job.capturedProfile.resourceMode", 32) + `,
 'limits',json(CASE WHEN length(CAST(json_extract(j.outcome_json,'$.job.capturedProfile.limits') AS BLOB))<=1024 THEN json_extract(j.outcome_json,'$.job.capturedProfile.limits') ELSE '{}' END)),
 'warnings',json(COALESCE((SELECT json_group_array(json_object('code',substr(json_extract(w.value,'$.code'),1,128))) FROM (SELECT value FROM json_each(j.outcome_json,'$.job.warnings') LIMIT 8) w),'[]'))),
 'snapshotState',` + text("$.snapshotState", 32) + `,'warningCount',COALESCE(json_array_length(j.outcome_json,'$.job.warnings'),0)) END`
}

// observationContext joins request and service cancellation without a lifecycle lock or polling goroutine.
func (m *Manager) observationContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	stop := context.AfterFunc(m.ctx, cancel)
	if m.ctx.Err() != nil {
		cancel()
	}
	return ctx, func() { stop(); cancel() }
}

// progressRows joins each sample and committed state in a single consistent SQL observation.
func progressRows() string {
	return `SELECT j.id,j.state,` + progressProjection() + `,CASE WHEN length(CAST(p.detail_json AS BLOB))<8192 THEN p.detail_json END FROM jobs j LEFT JOIN job_progress p ON p.job_id=j.id `
}

// Progress never waits for lifecycle ownership or opens a generation/source file.
func (m *Manager) Progress(parent context.Context, id string) (JobProgress, error) {
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	out, err := retryControlRead(ctx, func() (JobProgress, error) { return m.observeProgress(ctx, id) })
	return out, observationError(err)
}

// observeProgress keeps SQL errors internal; callers receive stable unavailable errors.
func (m *Manager) observeProgress(parent context.Context, id string) (JobProgress, error) {
	if !identifier.MatchString(id) {
		return JobProgress{}, ErrInvalid
	}
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	var storedID, state, raw string
	var detail sql.NullString
	err := m.readDB.QueryRowContext(ctx, progressRows()+`WHERE j.id=?`, id).Scan(&storedID, &state, &raw, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return JobProgress{}, ErrNotFound
	}
	if err != nil {
		return JobProgress{}, err
	}
	return decodeProgress(storedID, state, raw, detail)
}

// decodeProgress validates projected identity and never treats a damaged sample as measured.
func decodeProgress(id, state, raw string, detail sql.NullString) (JobProgress, error) {
	out := JobProgress{Entries: "0", Files: "0", Directories: "0", Specials: "0", UnknownLogical: "0", KnownLogicalBytes: "0", Warnings: "0", ErrorCodes: []string{}}
	var projected struct {
		Outcome
		WarningCount uint64 `json:"warningCount"`
	}
	if json.Unmarshal([]byte(raw), &projected) != nil {
		return out, ErrUnavailable
	}
	o := projected.Outcome
	if len(raw) > 8192 || json.Unmarshal([]byte(raw), &o) != nil || o.Job.ID != id || o.Job.State != state || !identifier.MatchString(o.Job.ProfileID) || !contracts.ValidScanMode(o.Job.ScanMode) || !validSummaryTime(o.Job.CreatedAt) || !validSummaryAttempt(ProfileAttempt{JobID: id, State: state, ScanMode: o.Job.ScanMode, StartedAt: o.Job.StartedAt, EndedAt: o.Job.EndedAt, SnapshotState: o.SnapshotState}) || (o.Job.Trigger != "ui" && o.Job.Trigger != "cli" && o.Job.Trigger != "scheduler") {
		return out, ErrUnavailable
	}
	out.CreatedAt = o.Job.CreatedAt
	out.StartedAt = o.Job.StartedAt
	out.EndedAt = o.Job.EndedAt
	for _, w := range o.Job.Warnings {
		out.ErrorCodes = append(out.ErrorCodes, w.Code)
	}
	out.JobID = id
	out.ProfileID = o.Job.ProfileID
	out.State = state
	out.Trigger = o.Job.Trigger
	out.Mode = o.Job.ScanMode
	out.PublishedComplete = state == "completed" && o.SnapshotState == "complete" && o.Job.ResultSnapshotID != nil
	if detail.Valid {
		var r progressRecord
		if json.Unmarshal([]byte(detail.String), &r) == nil && validProgressRecord(r) {

			// An intact preset must also match this job's immutable captured inputs.
			captured, policyErr := resources.Resolve(o.Job.CapturedProfile.ResourceMode, o.Job.CapturedProfile.Limits)
			if r.ResourcePolicy.valid() && policyErr == nil && *r.ResourcePolicy == *effectivePolicy(captured) {
				out.ResourcePolicy = r.ResourcePolicy
			}
			out.Measured = r.Observed
			if r.Observed {
				out.ObservedAt = &r.At
			}
			out.Estimate = r.Estimate
			out.TargetID = r.Sample.TargetID
			out.Entries = strconv.FormatUint(r.Sample.Entries, 10)
			out.Files = strconv.FormatUint(r.Sample.Files, 10)
			out.Directories = strconv.FormatUint(r.Sample.Directories, 10)
			out.Specials = strconv.FormatUint(r.Sample.Specials, 10)
			out.UnknownLogical = strconv.FormatUint(r.Sample.UnknownLogical, 10)
			out.KnownLogicalBytes = r.Sample.KnownLogicalBytes
			if r.Sample.Warnings > ^uint64(0)-projected.WarningCount {
				return out, ErrUnavailable
			}
			out.Warnings = strconv.FormatUint(r.Sample.Warnings+projected.WarningCount, 10)
		}
	}

	if o.Job.StartedAt != nil {
		start, e := time.Parse(time.RFC3339Nano, *o.Job.StartedAt)
		end := time.Now()
		if e == nil && o.Job.EndedAt != nil {
			end, e = time.Parse(time.RFC3339Nano, *o.Job.EndedAt)
		}
		if e == nil && !end.Before(start) {
			v := strconv.FormatInt(int64(end.Sub(start)/time.Second), 10)
			out.ElapsedSeconds = &v
		}
	}
	return out, nil
}

// JobHistory is a bounded presentation window; historical provenance remains intact.
type JobHistory struct {
	Jobs      []JobProgress `json:"jobs"`
	Truncated bool          `json:"truncated"`
}

// History bounds both rows and bytes and does not serialize unrelated readers or workers.
func (m *Manager) History(parent context.Context, profile string) (JobHistory, error) {
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	out, err := retryControlRead(ctx, func() (JobHistory, error) { return m.observeHistory(ctx, profile) })
	return out, observationError(err)
}

// observeHistory returns one statement snapshot before its public wrapper sanitizes failures.
func (m *Manager) observeHistory(parent context.Context, profile string) (JobHistory, error) {
	out := JobHistory{Jobs: []JobProgress{}}
	if !identifier.MatchString(profile) {
		return out, ErrInvalid
	}
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	rows, err := m.readDB.QueryContext(ctx, progressRows()+`WHERE json_extract(j.outcome_json,'$.job.profileId')=? ORDER BY j.rowid DESC LIMIT 51`, profile)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	budget := 48 * 1024
	for rows.Next() {
		var id, state, raw string
		var detail sql.NullString
		if err = rows.Scan(&id, &state, &raw, &detail); err != nil {
			return out, err
		}
		if len(out.Jobs) == 50 {
			out.Truncated = true
			break
		}
		p, e := decodeProgress(id, state, raw, detail)
		if e != nil {
			return out, e
		}
		if p.ProfileID != profile {
			return out, ErrUnavailable
		}
		p.TargetID = ""
		encoded, _ := json.Marshal(p)
		if len(encoded) > budget {
			out.Truncated = true
			break
		}
		budget -= len(encoded) + 1
		out.Jobs = append(out.Jobs, p)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// validProgressRecord rejects corrupt timestamps/counts instead of manufacturing a current zero sample.
func validProgressRecord(r progressRecord) bool {
	if contracts.Bytes(r.Sample.KnownLogicalBytes).Validate() != nil {
		return false
	}
	if r.Observed && !validSummaryTime(r.At) {
		return false
	}
	if r.Sample.TargetID != "" && !identifier.MatchString(r.Sample.TargetID) {
		return false
	}
	if r.Sample.Files > r.Sample.Entries || r.Sample.Directories > r.Sample.Entries || r.Sample.Specials > r.Sample.Entries || r.Sample.UnknownLogical > r.Sample.Files || r.Sample.Directories > r.Sample.Entries-r.Sample.Files || r.Sample.Specials > r.Sample.Entries-r.Sample.Files-r.Sample.Directories {
		return false
	}
	if r.Estimate != nil {
		var n uint64
		n, e := strconv.ParseUint(r.Estimate.Entries, 10, 64)
		if e != nil || fmt.Sprint(n) != r.Estimate.Entries || r.Estimate.Basis != "previous-complete" || !identifier.MatchString(r.Estimate.SnapshotID) {
			return false
		}
	}
	return true
}

// observationError maps cooperative deadline/SQL/close failures to the existing service-unavailable response.
func observationError(err error) error {
	if err == nil || errors.Is(err, ErrInvalid) || errors.Is(err, ErrNotFound) {
		return err
	}
	return ErrUnavailable
}
