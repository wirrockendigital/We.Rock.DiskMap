package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// ObserveJob is the existing HTTP job read-side, without lifecycle locks or generation I/O.
// Complete publication is a durable outcome; current index readability remains the reader's responsibility.
func (m *Manager) ObserveJob(parent context.Context, id string) (Outcome, error) {
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	out, err := retryControlRead(ctx, func() (Outcome, error) { return m.observeJob(ctx, id) })
	return out, observationError(err)
}

// observeJob owns the bounded transactional implementation behind the public error mapping.
func (m *Manager) observeJob(parent context.Context, id string) (Outcome, error) {
	var out Outcome
	if !identifier.MatchString(id) {
		return out, ErrInvalid
	}
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	tx, err := m.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var raw, state string
	// Strip only private source authority, already excluded by the HTTP serializer. Public captured fields remain intact.
	err = tx.QueryRowContext(ctx, `SELECT state,CASE WHEN length(CAST(json_remove(outcome_json,'$.scopeManifest.provenance') AS BLOB))<=262144 THEN json_remove(outcome_json,'$.scopeManifest.provenance') END FROM jobs WHERE id=?`, id).Scan(&state, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrInvalid
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ID != id || out.Job.State != state || !identifier.MatchString(out.Job.ProfileID) || !contracts.ValidScanMode(out.Job.ScanMode) {
		return Outcome{}, ErrUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT state,entered_at FROM job_phases WHERE job_id=? ORDER BY sequence LIMIT 9`, id)
	if err != nil {
		return out, err
	}
	out.PhaseHistory = []JobPhase{}
	for rows.Next() {
		var p JobPhase
		if err = rows.Scan(&p.State, &p.EnteredAt); err != nil {
			break
		}
		out.PhaseHistory = append(out.PhaseHistory, p)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.PhaseHistory) > 8 {
		return out, ErrUnavailable
	}
	out.Control = nil
	var control JobControlStatus
	err = tx.QueryRowContext(ctx, `SELECT requested,version FROM job_controls WHERE job_id=?`, id).Scan(&control.Requested, &control.Version)
	if err == nil {
		out.Control = &control
	} else if !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	stored := StoredDataStatus{Quality: "missing", Freshness: "unknown"}
	var snap, quality sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT a.snapshot_id,i.quality FROM active_generations a LEFT JOIN integrity_generations i ON i.snapshot_id=a.snapshot_id AND i.profile_id=a.profile_id WHERE a.profile_id=?`, out.Job.ProfileID).Scan(&snap, &quality)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if snap.Valid && snap.String != "" {
		if !identifier.MatchString(snap.String) {
			return out, ErrUnavailable
		}
		stored.SnapshotID = &snap.String
		stored.Quality = "unverified"
		if quality.Valid {
			switch quality.String {
			case "corrupt", "unsupported", "unverified":
				stored.Quality = quality.String
			case "deleting", "deleted":
				stored.Quality = "missing"
			case "healthy", "unknown":
			default:
				return out, ErrUnavailable
			}
		}
	}
	out.StoredData = &stored
	if err = tx.Commit(); err != nil {
		return Outcome{}, err
	}
	return out, nil
}
