package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// WaitStatus reads the committed job without lifecycle locks or generation filesystem access.
// database/sql coordinates concurrent close and writes; the immutable handle is never replaced.
func (m *Manager) WaitStatus(ctx context.Context, id string) (Outcome, error) {
	var out Outcome
	if !security.ValidID(id) {
		return out, ErrInvalid
	}
	work, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw, state string
	if err := m.db.QueryRowContext(work, `SELECT state,outcome_json FROM jobs WHERE id=? AND length(CAST(outcome_json AS BLOB))<=1048576`, id).Scan(&state, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrInvalid
		}
		return out, ErrUnavailable
	}
	if json.Unmarshal([]byte(raw), &out) != nil || out.Job.ID != id || out.Job.State != state || !security.ValidID(out.Job.ProfileID) || !contracts.ValidScanMode(out.Job.ScanMode) {
		return Outcome{}, ErrUnavailable
	}
	// Enrichment belongs to interactive readers, never to the reliable terminal wait path.
	out.PhaseHistory = nil
	out.StoredData = nil
	out.Control = nil
	return out, nil
}
