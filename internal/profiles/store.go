// Package profiles owns canonical profile persistence inside the shared control database.
package profiles

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// Typed sentinel errors never include source names, paths or profile contents.
var (
	ErrInvalid  = errors.New("INVALID_PROFILE")
	ErrConflict = errors.New("PROFILE_REVISION_CONFLICT")
	ErrNotFound = errors.New("PROFILE_NOT_FOUND")
	ErrInUse    = errors.New("PROFILE_IN_USE")
	ErrRetired  = errors.New("PROFILE_ID_RETIRED")
)

// Record keeps private bindings alongside the public canonical configuration.
// Callers must never serialize this internal storage object to a browser.
type Record struct {
	Profile                contracts.Profile
	RootsJSON, VolumesJSON string
}

// Summary joins references without rewriting historical captured configurations.
type Summary struct {
	Profile          contracts.Profile `json:"profile"`
	ActiveSnapshotID *string           `json:"activeSnapshotId"`
	LatestJobID      *string           `json:"latestJobId"`
}

// Initialize is an additive schema operation; existing profiles and generations remain unchanged.
func Initialize(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS profiles(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,profile_json TEXT NOT NULL,roots_json TEXT NOT NULL,volumes_json TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS profile_tombstones(id TEXT PRIMARY KEY NOT NULL);`)
	return err
}

// NewID generates a technical identifier independent from display names and source paths.
func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// Validate checks persistence shape; scanner capability enforcement remains at job start.
// Known IDs come from authoritative inventory or the explicitly isolated fixture import.
func Validate(p contracts.Profile, known map[string]bool) error {
	if !security.ValidID(p.ID) || !utf8.ValidString(p.Name) || strings.TrimSpace(p.Name) == "" || len(p.Name) > 1024 || p.FreshnessSeconds < 0 || p.Limits.Workers < 1 || p.Limits.Workers > 64 {
		return ErrInvalid
	}
	for _, r := range p.Name {
		if unicode.IsControl(r) {
			return ErrInvalid
		}
	}
	switch p.ResourceMode {
	case "gentle", "balanced", "fast", "custom":
	default:
		return ErrInvalid
	}
	if p.Limits.CPUPercent != nil && (*p.Limits.CPUPercent < 1 || *p.Limits.CPUPercent > 100) {
		return ErrInvalid
	}
	if p.Limits.IOBytesPerSecond != nil && p.Limits.IOBytesPerSecond.Validate() != nil {
		return ErrInvalid
	}
	if !contracts.ValidFolderScope(p.Scope) {
		return ErrInvalid
	}
	switch p.Scope.Kind {
	case "nas":
		if len(p.Scope.TargetIDs) != 0 {
			return ErrInvalid
		}
	case "volumes", "shares", "mixed", "folder":
		if len(p.Scope.TargetIDs) == 0 || len(p.Scope.TargetIDs) > 256 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range p.Scope.TargetIDs {
		if !security.ValidID(id) || !known[id] || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	if _, err := CompileExclusions(p.Exclusions); err != nil {
		return err
	}

	return nil
}

// Read loads one canonical profile and verifies its redundant indexed identity/version.
func Read(ctx context.Context, tx *sql.Tx, id string) (Record, error) {
	var r Record
	var raw string
	var revision int
	if !security.ValidID(id) {
		return r, ErrInvalid
	}
	err := tx.QueryRowContext(ctx, `SELECT revision,profile_json,roots_json,volumes_json FROM profiles WHERE id=?`, id).Scan(&revision, &raw, &r.RootsJSON, &r.VolumesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if json.Unmarshal([]byte(raw), &r.Profile) != nil || r.Profile.ID != id || r.Profile.Revision != revision || revision < 1 {
		return r, ErrInvalid
	}
	return r, nil
}

// Write performs an optimistic update inside the caller's domain/replay transaction.
// Create uses expected=0; pure name/activation changes preserve the scan configuration version.
func Write(ctx context.Context, tx *sql.Tx, r Record, expected int, known map[string]bool) (contracts.Profile, error) {
	p := r.Profile
	if expected < 0 || Validate(p, known) != nil || !json.Valid([]byte(r.RootsJSON)) || !json.Valid([]byte(r.VolumesJSON)) {
		return p, ErrInvalid
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM profile_tombstones WHERE id=?`, p.ID).Scan(&retired); err != nil {
		return p, err
	}
	if retired > 0 {
		return p, ErrRetired
	}
	old, err := Read(ctx, tx, p.ID)
	if expected == 0 {
		if err == nil {
			return p, ErrConflict
		}
		if !errors.Is(err, ErrNotFound) {
			return p, err
		}
		p.Revision = 1
	} else {
		if err != nil {
			return p, err
		}
		if old.Profile.Revision != expected {
			return p, ErrConflict
		}
		p.Revision = expected
		// Names and activation affect presentation/eligibility, not the meaning of existing measurements.
		a, b := canonical(old.Profile), canonical(p)
		a.Name = ""
		b.Name = ""
		a.Active = false
		b.Active = false
		a.Revision = 0
		b.Revision = 0
		if !reflect.DeepEqual(a, b) {
			p.Revision++
			if p.Revision <= expected {
				return p, ErrInvalid
			}
		}
	}
	return p, put(ctx, tx, Record{Profile: p, RootsJSON: r.RootsJSON, VolumesJSON: r.VolumesJSON})
}

// Import preserves the privileged bootstrap's explicit revision/rebinding contract.
func Import(ctx context.Context, tx *sql.Tx, r Record, known map[string]bool) error {
	if r.Profile.Revision < 1 || Validate(r.Profile, known) != nil || !json.Valid([]byte(r.RootsJSON)) || !json.Valid([]byte(r.VolumesJSON)) {
		return ErrInvalid
	}
	var retired int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM profile_tombstones WHERE id=?`, r.Profile.ID).Scan(&retired); err != nil {
		return err
	}
	if retired > 0 {
		return ErrRetired
	}
	old, err := Read(ctx, tx, r.Profile.ID)
	if err == nil && old.Profile.Revision >= r.Profile.Revision {
		return ErrConflict
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return put(ctx, tx, r)
}

// put keeps the storage representation singular; no parallel JSON profile files exist.
func put(ctx context.Context, tx *sql.Tx, r Record) error {
	raw, err := json.Marshal(r.Profile)
	if err != nil {
		return ErrInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO profiles(id,revision,profile_json,roots_json,volumes_json) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision,profile_json=excluded.profile_json,roots_json=excluded.roots_json,volumes_json=excluded.volumes_json`, r.Profile.ID, r.Profile.Revision, string(raw), r.RootsJSON, r.VolumesJSON)
	return err
}

// DeleteUnused retires only unreferenced configuration and never touches source or generation files.
// Product deletion confirmation and retention behavior belong to the profile-actions slice.
func DeleteUnused(ctx context.Context, tx *sql.Tx, id string, expected int) error {
	r, err := Read(ctx, tx, id)
	if err != nil {
		return err
	}
	if r.Profile.Revision != expected {
		return ErrConflict
	}
	var used int
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM active_generations WHERE profile_id=?) OR EXISTS(SELECT 1 FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=?)`, id, id).Scan(&used); err != nil {
		return err
	}
	if used != 0 {
		return ErrInUse
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO profile_tombstones VALUES(?)`, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM profile_sources WHERE profile_id=?`, id); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM profiles WHERE id=?`, id)
	return err
}

// List returns bounded public configurations with their latest job and complete generation references.
func List(ctx context.Context, db *sql.DB) ([]Summary, error) {
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.revision,p.profile_json,a.snapshot_id,(SELECT j.id FROM jobs j WHERE json_extract(j.outcome_json,'$.job.profileId')=p.id ORDER BY j.rowid DESC LIMIT 1) FROM profiles p LEFT JOIN active_generations a ON a.profile_id=p.id ORDER BY p.id LIMIT 201`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		if len(out) >= 200 {
			return nil, ErrInvalid
		}
		var id, raw string
		var rev int
		var snapshot, job sql.NullString
		if err = rows.Scan(&id, &rev, &raw, &snapshot, &job); err != nil {
			return nil, err
		}
		var s Summary
		if json.Unmarshal([]byte(raw), &s.Profile) != nil || s.Profile.ID != id || s.Profile.Revision != rev {
			return nil, ErrInvalid
		}
		if snapshot.Valid {
			if !security.ValidID(snapshot.String) {
				return nil, ErrInvalid
			}
			s.ActiveSnapshotID = &snapshot.String
		}
		if job.Valid {
			if !security.ValidID(job.String) {
				return nil, ErrInvalid
			}
			s.LatestJobID = &job.String
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// canonical removes representation-only differences without mutating caller-owned slices.
func canonical(p contracts.Profile) contracts.Profile {
	p.Scope.TargetIDs = append([]string{}, p.Scope.TargetIDs...)
	sort.Strings(p.Scope.TargetIDs)
	p.Exclusions = append([]contracts.Exclusion{}, p.Exclusions...)
	return p
}
