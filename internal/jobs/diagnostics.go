package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"runtime"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// DiagnosticStatus contains only bounded, non-identifying component observations.
// Recorded integrity counts are historical observations, never a fresh integrity claim.
type DiagnosticStatus struct {
	StatusVersion   int                    `json:"statusVersion"`
	ObservedAt      string                 `json:"observedAt"`
	Backend         string                 `json:"backend"`
	Phase           string                 `json:"phase"`
	ElapsedMillis   *uint64                `json:"elapsedMillis"`
	Warnings        *uint64                `json:"warnings"`
	Queue           DiagnosticQueue        `json:"queue"`
	ControlDatabase DiagnosticDatabase     `json:"controlDatabase"`
	Storage         DiagnosticStorage      `json:"storage"`
	Generations     DiagnosticGenerations  `json:"generations"`
	Versions        DiagnosticVersions     `json:"versions"`
	Capabilities    DiagnosticCapabilities `json:"capabilities"`
	ReasonCodes     []string               `json:"reasonCodes"`
}

// DiagnosticQueue reports accepted waiting work, bounded by the queue contract.
type DiagnosticQueue struct {
	State    string `json:"state"`
	Waiting  *int   `json:"waiting"`
	Capacity int    `json:"capacity"`
}

// DiagnosticDatabase separates successful SQL access from unperformed integrity checking.
type DiagnosticDatabase struct {
	Availability string `json:"availability"`
	Integrity    string `json:"integrity"`
}

// DiagnosticStorage measures available package-state capacity, not source capacity or forecast demand.
type DiagnosticStorage struct {
	State        string           `json:"state"`
	FreeBytes    *contracts.Bytes `json:"freeBytes"`
	ReserveBytes contracts.Bytes  `json:"reserveBytes"`
}

// DiagnosticGenerations samples at most 128 persisted observations without opening any generation.
type DiagnosticGenerations struct {
	Observation string `json:"observation"`
	Sampled     int    `json:"sampled"`
	Truncated   bool   `json:"truncated"`
	Healthy     int    `json:"healthy"`
	Corrupt     int    `json:"corrupt"`
	Unsupported int    `json:"unsupported"`
	Unverified  int    `json:"unverified"`
	Deleting    int    `json:"deleting"`
	Deleted     int    `json:"deleted"`
}

// DiagnosticVersions describes the running code's supported formats.
type DiagnosticVersions struct {
	Schema    uint64 `json:"schema"`
	Scanner   uint64 `json:"scanner"`
	Aggregate uint64 `json:"aggregate"`
	Go        string `json:"go"`
}

// DiagnosticCapabilities distinguishes implemented APIs from current scan availability and unobserved DSM UI capabilities.
type DiagnosticCapabilities struct {
	LocalAPI         bool    `json:"localAPI"`
	SnapshotReads    bool    `json:"snapshotReads"`
	JobControl       bool    `json:"jobControl"`
	IntegrityChecks  bool    `json:"integrityChecks"`
	ScanAvailable    *bool   `json:"scanAvailable"`
	DSMFileStation   string  `json:"dsmFileStation"`
	DSMBuild         *string `json:"dsmBuild"`
	DSMScheduledScan string  `json:"dsmScheduledScan"`
}

// DiagnosticStatus avoids waiting behind maintenance and never walks source or index files.
// SQL work has a separate short deadline; no SQL runs under the lifecycle mutex.
func (m *Manager) DiagnosticStatus(ctx context.Context) (DiagnosticStatus, error) {
	scannerVersion, _ := strconv.ParseUint(index.ScannerVersion, 10, 64)
	out := DiagnosticStatus{StatusVersion: 1, ObservedAt: now(), Backend: "busy", Phase: "unknown", ReasonCodes: []string{}, Queue: DiagnosticQueue{State: "unavailable", Capacity: QueueCapacity}, ControlDatabase: DiagnosticDatabase{Availability: "unavailable", Integrity: "unknown"}, Storage: DiagnosticStorage{State: "unavailable", ReserveBytes: contracts.Bytes(strconv.FormatUint(IndexReserveBytes, 10))}, Generations: DiagnosticGenerations{Observation: "unavailable"}, Versions: DiagnosticVersions{Schema: index.GenerationSchema, Scanner: scannerVersion, Aggregate: index.AggregateVersion, Go: runtime.Version()}, Capabilities: DiagnosticCapabilities{LocalAPI: true, SnapshotReads: true, JobControl: true, IntegrityChecks: true, DSMFileStation: "unknown", DSMScheduledScan: "unknown"}}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.mu.TryLock() {
		out.ReasonCodes = append(out.ReasonCodes, "COMPONENT_BUSY")
		return out, nil
	}
	db, state, probe, closed := m.db, m.state, m.spaceProbe, m.closed
	activeID := ""
	if m.active != nil {
		activeID = m.active.Job.ID
	}
	stopping := closed || (m.ctx != nil && m.ctx.Err() != nil)
	m.mu.Unlock()
	if stopping {
		out.Backend = "stopping"
		v := false
		out.Capabilities.ScanAvailable = &v
		out.ReasonCodes = append(out.ReasonCodes, "BACKEND_STOPPING")
		return out, nil
	}
	out.Backend = "available"
	work, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	if db == nil || db.PingContext(work) != nil {
		out.ReasonCodes = append(out.ReasonCodes, "CONTROL_DATABASE_UNAVAILABLE")
	} else {
		out.ControlDatabase.Availability = "available"
		var waiting int
		if db.QueryRowContext(work, `SELECT count(*) FROM (SELECT 1 FROM scan_queue LIMIT 17)`).Scan(&waiting) == nil && waiting <= QueueCapacity {
			out.Queue.State = "available"
			out.Queue.Waiting = &waiting
		} else {
			out.ReasonCodes = append(out.ReasonCodes, "QUEUE_UNAVAILABLE")
		}
		if activeID == "" {
			out.Phase = "idle"
		} else {
			out.diagnosticJob(work, db, activeID)
		}
		out.Generations = diagnosticGenerations(work, db)
	}
	// Statfs is one metadata operation on package state; it does not enumerate files.
	if ctx.Err() == nil {
		measure := Manager{state: state, spaceProbe: probe}
		if free, e := measure.availableStateBytes(); e == nil {
			b := contracts.Bytes(strconv.FormatUint(free, 10))
			out.Storage.FreeBytes = &b
			out.Storage.State = "available"
			if free < IndexReserveBytes {
				out.Storage.State = "low"
				out.ReasonCodes = append(out.ReasonCodes, "INDEX_SPACE_LOW")
			}
		} else {
			out.ReasonCodes = append(out.ReasonCodes, "INDEX_SPACE_UNAVAILABLE")
		}
	}
	if out.ControlDatabase.Availability == "available" && out.Queue.State == "available" && out.Storage.State != "unavailable" && out.Phase != "unknown" {
		v := out.Storage.State == "available" && *out.Queue.Waiting < QueueCapacity
		out.Capabilities.ScanAvailable = &v
	}
	return out, nil
}

// diagnosticJob accepts only known lifecycle values and numeric, non-private aggregates.
func (out *DiagnosticStatus) diagnosticJob(ctx context.Context, db *sql.DB, id string) {
	var phase string
	var started, ended sql.NullString
	var warnings sql.NullInt64
	e := db.QueryRowContext(ctx, `SELECT state,json_extract(outcome_json,'$.job.startedAt'),json_extract(outcome_json,'$.job.endedAt'),json_array_length(outcome_json,'$.job.warnings') FROM jobs WHERE id=?`, id).Scan(&phase, &started, &ended, &warnings)
	if e != nil {
		out.ReasonCodes = append(out.ReasonCodes, "SCANNER_UNAVAILABLE")
		return
	}
	switch phase {
	case "queued", "preflight", "scanning", "paused", "aggregating", "validating", "publishing", "completed", "failed", "cancelled", "interrupted":
		out.Phase = phase
	default:
		out.ReasonCodes = append(out.ReasonCodes, "SCANNER_UNAVAILABLE")
		return
	}
	if started.Valid {
		if at, e := time.Parse(time.RFC3339Nano, started.String); e == nil && !at.After(time.Now()) {
			until := time.Now()
			valid := true
			if ended.Valid {
				parsed, err := time.Parse(time.RFC3339Nano, ended.String)
				valid = err == nil && !parsed.Before(at) && !parsed.After(until)
				until = parsed
			}
			if valid {
				elapsed := uint64(until.Sub(at).Milliseconds())
				out.ElapsedMillis = &elapsed
			}
		}
	}
	if warnings.Valid && warnings.Int64 >= 0 {
		v := uint64(warnings.Int64)
		out.Warnings = &v
	}
	// Decode only a bounded progress record so uint64 warning totals never pass through float64.
	var raw string
	if db.QueryRowContext(ctx, `SELECT detail_json FROM job_progress WHERE job_id=? AND length(detail_json)<=8192`, id).Scan(&raw) == nil {
		var detail progressRecord
		if json.Unmarshal([]byte(raw), &detail) == nil && detail.Observed && out.Warnings != nil {
			if detail.Sample.Warnings <= math.MaxUint64-*out.Warnings {
				v := detail.Sample.Warnings + *out.Warnings
				out.Warnings = &v
			} else {
				out.Warnings = nil
			}
		}
	}

}

// diagnosticGenerations deliberately labels stored quality observations rather than revalidating files.
func diagnosticGenerations(ctx context.Context, db *sql.DB) DiagnosticGenerations {
	out := DiagnosticGenerations{Observation: "unavailable"}
	rows, e := db.QueryContext(ctx, `SELECT quality FROM integrity_generations ORDER BY rowid LIMIT 129`)
	if e != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var q string
		if rows.Scan(&q) != nil {
			return DiagnosticGenerations{Observation: "unavailable"}
		}
		if out.Sampled == 128 {
			out.Truncated = true
			break
		}
		out.Sampled++
		switch q {
		case "healthy":
			out.Healthy++
		case "corrupt":
			out.Corrupt++
		case "unsupported":
			out.Unsupported++
		case "unverified":
			out.Unverified++
		case "deleting":
			out.Deleting++
		case "deleted":
			out.Deleted++
		default:
			return DiagnosticGenerations{Observation: "unavailable"}
		}
	}
	if rows.Err() != nil {
		return DiagnosticGenerations{Observation: "unavailable"}
	}
	out.Observation = "recorded"
	return out
}
