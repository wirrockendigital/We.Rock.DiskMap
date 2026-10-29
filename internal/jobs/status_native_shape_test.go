package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"modernc.org/sqlite"
)

// Native nanosecond timestamps and a live UI job must survive the actual separate read-only pool.
func TestStatusSnapshotNativeLiveShape(t *testing.T) {
	m := statusFixture(t)
	var seq int
	var database, filename string
	if err := m.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &database, &filename); err != nil {
		t.Fatal(err)
	}
	pool, err := openControlReadPool(context.Background(), filepath.Dir(filename))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	m.readDB = pool
	started := "2026-10-13T22:27:35.976995935Z"
	job := contracts.Job{ID: "5af5aa1c578d19b3bc5123c57b17eec1", ProfileID: "a57693121e6f1efa78519badbcf58a54", ProfileRevision: 1, Trigger: "ui", ScanMode: "full", State: "scanning", EntriesVisited: "0", BytesObserved: "0", CreatedAt: "2026-10-13T22:27:35.758987366Z", StartedAt: &started, CapturedProfile: contracts.Profile{ID: "a57693121e6f1efa78519badbcf58a54", Revision: 1}}
	raw, err := json.Marshal(Outcome{Job: job})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`INSERT INTO jobs(id,state,outcome_json) VALUES(?,?,?)`, job.ID, job.State, string(raw)); err != nil {
		t.Fatal(err)
	}
	// Calling the inner read preserves a SQL/scan error that the public boundary intentionally sanitizes.
	got, err := m.observeStatus(context.Background())
	if err != nil {
		t.Fatalf("status SQL/qualification: %v", err)
	}
	if !ValidStatusSnapshot(got) || got.Active == nil || got.Active.JobID != job.ID || len(got.Queued) != 0 {
		t.Fatalf("invalid live result: %+v", got)
	}
	if _, err = m.Status(context.Background()); err != nil {
		t.Fatalf("public status: %v", err)
	}
}

// TestNativeStatusReadOnlyProbe is opt-in and never opens a writer, manager lifecycle, or source path.
// Diagnostic success means only that the probe finished; individual read errors remain in its receipt.
func TestNativeStatusReadOnlyProbe(t *testing.T) {
	state := os.Getenv("DISKMAP_STATUS_PROBE_STATE")
	if state == "" {
		t.Skip("explicit native read-only admission required")
	}
	id := os.Getenv("DISKMAP_STATUS_PROBE_JOB")
	if runtime.GOOS != "linux" || state != "/volume1/@appdata/we-rock-diskmap" || !security.ValidID(id) {
		t.Fatal("PROBE_ADMISSION")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	pool, err := openControlReadPool(ctx, state)
	if err != nil {
		t.Fatalf("PROBE_OPEN %s", statusProbeError(err))
	}
	defer pool.Close()
	m := &Manager{readDB: pool, ctx: ctx}
	measure := func(stage string, fn func() error) {
		start := time.Now()
		e := fn()
		t.Logf("stage=%s elapsed_ms=%.3f error=%s", stage, float64(time.Since(start).Microseconds())/1000, statusProbeError(e))
	}
	for round := 0; round < 3; round++ {
		t.Logf("round=%d utc=%s", round, time.Now().UTC().Format(time.RFC3339Nano))
		measure("observe-status", func() error { _, e := m.observeStatus(ctx); return e })
		measure("public-status", func() error { _, e := m.Status(ctx); return e })
		measure("observe-progress", func() error { _, e := m.observeProgress(ctx, id); return e })
		measure("public-progress", func() error { _, e := m.Progress(ctx, id); return e })
		if ctx.Err() != nil {
			break
		}
	}
	// Replay the exact six-column projection to distinguish SQL/Scan failures from public qualification.
	measure("status-projection", func() error {
		readCtx, stop := m.observationContext(ctx)
		defer stop()
		rows, e := pool.QueryContext(readCtx, `SELECT CASE WHEN length(CAST(j.id AS BLOB))<=128 THEN j.id END,CASE WHEN length(CAST(j.state AS BLOB))<=32 THEN j.state END,`+statusProjection()+`,q.sequence,CASE WHEN length(CAST(q.profile_id AS BLOB))<=128 THEN q.profile_id END FROM jobs j INDEXED BY jobs_status_live LEFT JOIN scan_queue q ON q.job_id=j.id WHERE `+liveStatusPredicate+` ORDER BY j.state,j.id LIMIT 18`)
		if e != nil {
			return e
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var rowID, state, raw string
			var bound int
			var sequence sql.NullInt64
			var profile sql.NullString
			if e = rows.Scan(&rowID, &state, &raw, &bound, &sequence, &profile); e != nil {
				return e
			}
			var job StatusJob
			decode := json.Unmarshal([]byte(raw), &job)
			count++
			queued := state == "queued"
			if queued {
				job.QueuePosition = 1
			}
			t.Logf("projection_row=%d bound=%t decode=%t identity=%t state_match=%t queued=%t sequence_present=%t profile_match=%t valid_job=%t", count, bound == 1, decode == nil, job.JobID == rowID, job.State == state, queued, sequence.Valid, profile.Valid && profile.String == job.ProfileID, validStatusJob(job, queued))
		}
		t.Logf("projection_count=%d", count)
		return rows.Err()
	})
}

// statusProbeError emits fixed classes and numeric SQLite codes without raw error messages or private values.
func statusProbeError(err error) string {
	if err == nil {
		return "none"
	}
	var sqliteError *sqlite.Error
	if errors.As(err, &sqliteError) {
		return fmt.Sprintf("sqlite:%d", sqliteError.Code())
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	case errors.Is(err, ErrNotFound):
		return "not-found"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, sql.ErrNoRows):
		return "no-rows"
	}
	return fmt.Sprintf("go-type:%T", err)
}
