package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ProfileSummariesRequest contains only explicit catalogue identities, never source paths.
type ProfileSummariesRequest struct {
	ContractVersion int      `json:"contractVersion"`
	ProfileIDs      []string `json:"profileIds"`
}

// Validate rejects duplicate identities and oversized batches before database access.
func (r ProfileSummariesRequest) Validate() error {
	if r.ContractVersion != 1 || len(r.ProfileIDs) == 0 || len(r.ProfileIDs) > 32 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range r.ProfileIDs {
		if !security.ValidID(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

// ProfileAttempt is independent of the retained complete publication.
type ProfileAttempt struct {
	JobID         string  `json:"jobId"`
	State         string  `json:"state"`
	ScanMode      string  `json:"scanMode"`
	StartedAt     *string `json:"startedAt"`
	EndedAt       *string `json:"endedAt"`
	SnapshotState string  `json:"snapshotState"`
}

// ProfileSummary reports committed provenance and explicitly historical integrity observations.
type ProfileSummary struct {
	ProfileID          string           `json:"profileId"`
	CurrentRevision    uint64           `json:"currentRevision"`
	SnapshotID         *string          `json:"snapshotId"`
	CapturedRevision   *uint64          `json:"capturedRevision"`
	EndedAt            *string          `json:"endedAt"`
	Quality            string           `json:"quality"`
	Freshness          string           `json:"freshness"`
	AgeSeconds         *contracts.Bytes `json:"ageSeconds"`
	IntegrityQuality   string           `json:"integrityQuality"`
	IntegrityCheckedAt *string          `json:"integrityCheckedAt"`
	LastAttempt        *ProfileAttempt  `json:"lastAttempt"`
}

// ProfileSummaries binds all card observations to the same control snapshot.
type ProfileSummaries struct {
	ObservedAt string           `json:"observedAt"`
	Profiles   []ProfileSummary `json:"profiles"`
}

// ProfileSummaries reads one consistent control transaction without lifecycle locks or index I/O.
// Publication commits the active pointer and complete outcome together; this is recorded quality,
// not a claim that the underlying index file is currently readable or has just passed integrity.
func (m *Manager) ProfileSummaries(ctx context.Context, in ProfileSummariesRequest) (ProfileSummaries, error) {
	out := ProfileSummaries{ObservedAt: now(), Profiles: make([]ProfileSummary, 0, len(in.ProfileIDs))}
	if e := in.Validate(); e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	tx, e := m.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, ErrUnavailable
	}
	defer tx.Rollback()
	observed, _ := time.Parse(time.RFC3339Nano, out.ObservedAt)
	for _, id := range in.ProfileIDs {
		s := ProfileSummary{ProfileID: id, Quality: "missing", Freshness: "unknown", IntegrityQuality: "unknown"}
		var revision int64
		var freshness int64
		var snap, iq, checked sql.NullString
		e = tx.QueryRowContext(ctx, `SELECT p.revision,json_extract(p.profile_json,'$.freshnessSeconds'),a.snapshot_id,i.quality,i.checked_at FROM profiles p LEFT JOIN active_generations a ON a.profile_id=p.id LEFT JOIN integrity_generations i ON i.snapshot_id=a.snapshot_id AND i.profile_id=p.id WHERE p.id=? AND json_extract(p.profile_json,'$.id')=p.id AND json_extract(p.profile_json,'$.revision')=p.revision`, id).Scan(&revision, &freshness, &snap, &iq, &checked)
		if errors.Is(e, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		if e != nil || revision < 1 || freshness < 0 {
			return out, ErrUnavailable
		}
		s.CurrentRevision = uint64(revision)
		// Only the bounded metadata projection crosses the SQL boundary; roots and warnings stay private.
		var latest string
		e = tx.QueryRowContext(ctx, `SELECT CASE WHEN json_extract(outcome_json,'$.job.id')=id AND json_extract(outcome_json,'$.job.state')=state THEN json_object('jobId',id,'state',state,'scanMode',json_extract(outcome_json,'$.job.scanMode'),'startedAt',json_extract(outcome_json,'$.job.startedAt'),'endedAt',json_extract(outcome_json,'$.job.endedAt'),'snapshotState',json_extract(outcome_json,'$.snapshotState')) ELSE NULL END FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=? ORDER BY rowid DESC LIMIT 1`, id).Scan(&latest)
		if e == nil {
			if len(latest) > 4096 || json.Unmarshal([]byte(latest), &s.LastAttempt) != nil || s.LastAttempt == nil || !validSummaryAttempt(*s.LastAttempt) {
				return out, ErrUnavailable
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return out, ErrUnavailable
		}
		if snap.Valid {
			if !security.ValidID(snap.String) {
				return out, ErrUnavailable
			}
			s.SnapshotID = &snap.String
			s.Quality = "unverified"
			var captured int64
			var end sql.NullString
			e = tx.QueryRowContext(ctx, `SELECT json_extract(outcome_json,'$.job.profileRevision'),json_extract(outcome_json,'$.snapshotEndedAt') FROM jobs WHERE state='completed' AND json_extract(outcome_json,'$.job.state')=state AND json_extract(outcome_json,'$.job.id')=id AND json_extract(outcome_json,'$.job.profileId')=? AND json_extract(outcome_json,'$.job.resultSnapshotId')=? AND json_extract(outcome_json,'$.snapshotState')='complete' ORDER BY rowid DESC LIMIT 1`, id, snap.String).Scan(&captured, &end)
			if e == nil && captured > 0 {
				v := uint64(captured)
				s.CapturedRevision = &v
				s.Quality = "complete"
				if end.Valid && validSummaryTime(end.String) {
					s.EndedAt = &end.String
					ended, _ := time.Parse(time.RFC3339Nano, end.String)
					age := observed.Sub(ended)
					if age >= 0 {
						seconds := contracts.Bytes(strconv.FormatInt(int64(age/time.Second), 10))
						s.AgeSeconds = &seconds
						if freshness > 0 {
							s.Freshness = "current"
							if captured != revision || int64(age/time.Second) > freshness || (int64(age/time.Second) == freshness && age%time.Second > 0) {
								s.Freshness = "stale"
							}
						}
					}
				}
			} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
				return out, ErrUnavailable
			}
			if iq.Valid {
				if !validIntegritySummary(iq.String) {
					return out, ErrUnavailable
				}
				s.IntegrityQuality = iq.String
				if checked.Valid {
					if !validSummaryTime(checked.String) {
						return out, ErrUnavailable
					}
					s.IntegrityCheckedAt = &checked.String
				}
				switch iq.String {
				case "corrupt", "unsupported", "unverified":
					s.Quality = iq.String
				case "deleting", "deleted":
					s.Quality = "missing"
				}
			}
		}
		out.Profiles = append(out.Profiles, s)
	}
	if e = tx.Commit(); e != nil {
		return out, ErrUnavailable
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > 60*1024 {
		return ProfileSummaries{}, ErrUnavailable
	}
	return out, nil
}

// Closed enums and timestamps keep corrupt persistence from becoming arbitrary UI text.
func validSummaryTime(s string) bool {
	_, e := time.Parse(time.RFC3339Nano, s)
	return e == nil && len(s) <= 40
}
func validIntegritySummary(s string) bool {
	switch s {
	case "unknown", "healthy", "corrupt", "unsupported", "unverified", "deleting", "deleted":
		return true
	}
	return false
}
func validSummaryAttempt(a ProfileAttempt) bool {
	if !security.ValidID(a.JobID) || !contracts.ValidScanMode(a.ScanMode) {
		return false
	}
	switch a.State {
	case "queued", "preflight", "scanning", "paused", "aggregating", "validating", "publishing", "completed", "failed", "cancelled", "interrupted":
	default:
		return false
	}
	if a.StartedAt != nil && !validSummaryTime(*a.StartedAt) || a.EndedAt != nil && !validSummaryTime(*a.EndedAt) {
		return false
	}
	return a.SnapshotState == "" || a.SnapshotState == "complete" || a.SnapshotState == "partial"
}

// ValidProfileSummaries binds the private response to the exact requested ordered batch.
func ValidProfileSummaries(out ProfileSummaries, in ProfileSummariesRequest) bool {
	if in.Validate() != nil || !validSummaryTime(out.ObservedAt) || len(out.Profiles) != len(in.ProfileIDs) {
		return false
	}
	for n, s := range out.Profiles {
		if s.ProfileID != in.ProfileIDs[n] || s.CurrentRevision == 0 || !validIntegritySummary(s.IntegrityQuality) {
			return false
		}
		switch s.Quality {
		case "missing", "unverified", "complete", "corrupt", "unsupported":
		default:
			return false
		}
		switch s.Freshness {
		case "unknown", "current", "stale":
		default:
			return false
		}
		if s.SnapshotID != nil && !security.ValidID(*s.SnapshotID) || s.CapturedRevision != nil && *s.CapturedRevision == 0 || s.EndedAt != nil && !validSummaryTime(*s.EndedAt) || s.IntegrityCheckedAt != nil && !validSummaryTime(*s.IntegrityCheckedAt) || s.LastAttempt != nil && !validSummaryAttempt(*s.LastAttempt) {
			return false
		}
		if s.AgeSeconds != nil {
			if value, e := strconv.ParseUint(string(*s.AgeSeconds), 10, 64); e != nil || strconv.FormatUint(value, 10) != string(*s.AgeSeconds) {
				return false
			}
		}
		if s.SnapshotID == nil && (s.CapturedRevision != nil || s.EndedAt != nil || s.Quality != "missing" || s.Freshness != "unknown" || s.AgeSeconds != nil || s.IntegrityQuality != "unknown" || s.IntegrityCheckedAt != nil) {
			return false
		}
		// Missing provenance cannot support age, freshness, or a complete publication claim.
		if s.EndedAt != nil && s.CapturedRevision == nil || s.AgeSeconds != nil && s.EndedAt == nil || s.Quality == "complete" && s.CapturedRevision == nil {
			return false
		}
		if s.Freshness != "unknown" && (s.SnapshotID == nil || s.CapturedRevision == nil || s.EndedAt == nil || s.AgeSeconds == nil) {
			return false
		}
		if s.Freshness == "current" && *s.CapturedRevision != s.CurrentRevision {
			return false
		}
		if s.IntegrityCheckedAt != nil && s.IntegrityQuality == "unknown" {
			return false
		}

	}
	return true
}
