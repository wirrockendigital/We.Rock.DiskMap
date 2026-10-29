package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"strconv"
	"time"
)

// storedDataStatus reads only the bound sealed header; the caller holds the manager lock.
func (m *Manager) storedDataStatus(ctx context.Context, profileID string) (StoredDataStatus, error) {
	out := StoredDataStatus{Quality: "missing", Freshness: "unknown"}
	snapshot, err := m.activeGeneration(ctx, profileID)
	if errors.Is(err, ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if snapshot == "" {
		return out, nil
	}
	out.SnapshotID = &snapshot
	// Missing access or an unfinished check is not evidence of corruption.
	out.Quality = "unverified"
	if value, ok := m.integrityStates.Load(snapshot); ok {
		quality := value.(integrityGuard).quality
		switch quality {
		case "corrupt", "unsupported", "unverified":
			out.Quality = quality
			return out, nil
		case "deleting", "deleted":
			out.Quality = "missing"
			return out, nil
		}
	}
	db, release, err := m.generationDB(snapshot)
	if err != nil {
		return out, nil
	}
	defer release()
	h, err := index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		out.Quality = integrityQuality(ctx, err)
		return out, nil
	}
	if h.Profile.ID != profileID || h.State != "complete" {
		out.Quality = "corrupt"
		return out, nil
	}
	out.Quality = "complete"
	out.EndedAt = &h.EndedAt
	var raw string
	if err = m.db.QueryRowContext(ctx, `SELECT profile_json FROM profiles WHERE id=?`, profileID).Scan(&raw); err != nil {
		return out, err
	}
	var profile contracts.Profile
	if json.Unmarshal([]byte(raw), &profile) != nil {
		return out, ErrUnavailable
	}
	ended, err := time.Parse(time.RFC3339Nano, h.EndedAt)
	if err != nil {
		return out, nil
	}
	age := time.Since(ended)
	if age < 0 {
		return out, nil
	}
	seconds := contracts.Bytes(strconv.FormatInt(int64(age/time.Second), 10))
	out.AgeSeconds = &seconds
	if profile.FreshnessSeconds <= 0 {
		return out, nil
	}
	out.Freshness = "current"
	// Compare whole seconds without converting an arbitrary profile threshold into a duration.
	wholeSeconds := int64(age / time.Second)
	if wholeSeconds > int64(profile.FreshnessSeconds) || (wholeSeconds == int64(profile.FreshnessSeconds) && age%time.Second > 0) {
		out.Freshness = "stale"
	}
	return out, nil
}
