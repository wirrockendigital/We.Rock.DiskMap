package jobs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"time"
)

// Upgrade owns offline schema operations; it never starts workers or traverses source storage.
func Upgrade(parent context.Context, state, mode string) (result error) {
	ctx, cancel := upgradeDeadline(parent, mode)
	defer cancel()
	// Classify a failed offline mode before cancelling our context; completed publication stays successful.
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = errors.Join(migration.ErrUpgrade, ctx.Err())
		}
	}()
	switch mode {
	case "prepare":
		return migration.Prepare(ctx, state)
	case "apply":
		return migration.Apply(ctx, state, func(ctx context.Context, db *sql.DB) error {
			if err := initializeControl(ctx, db); err != nil {
				return err
			}
			return qualifyUpgradePointers(ctx, db, state)
		})
	case "restore":
		return migration.Restore(ctx, state)
	default:
		return ErrInvalid
	}
}

// Apply verifies all active sealed generations; its bounded budget includes full index reads.
// Parent cancellation and earlier deadlines remain authoritative for every mode.
func upgradeDeadline(parent context.Context, mode string) (context.Context, context.CancelFunc) {
	if mode == "apply" {
		return context.WithTimeout(parent, 120*time.Minute)
	}
	return migration.Deadline(parent)
}

// qualifyUpgradePointers never rewrites sealed indexes; only proven unsupported/corrupt pointers are withdrawn.
func qualifyUpgradePointers(ctx context.Context, db *sql.DB, state string) error {
	cursor := ""
	for {
		var profile, snapshot, job string
		err := db.QueryRowContext(ctx, `SELECT a.profile_id,a.snapshot_id,coalesce((SELECT id FROM jobs j WHERE json_extract(j.outcome_json,'$.job.resultSnapshotId')=a.snapshot_id AND json_extract(j.outcome_json,'$.job.profileId')=a.profile_id AND state='completed' LIMIT 1),'') FROM active_generations a WHERE a.profile_id>? ORDER BY a.profile_id LIMIT 1`, cursor).Scan(&profile, &snapshot, &job)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		cursor = profile
		if !identifier.MatchString(profile) || !identifier.MatchString(snapshot) || !identifier.MatchString(job) {
			return ErrUnavailable
		}
		m := &Manager{state: state}
		gen, close, err := m.openGenerationDB(snapshot)
		if err != nil {
			return err
		}
		err = index.VerifyGeneration(ctx, gen, snapshot, profile, job)
		close()
		quality := ""
		if errors.Is(err, index.ErrUnsupportedGeneration) {
			quality = "unsupported"
		} else if errors.Is(err, index.ErrGeneration) {
			quality = "corrupt"
		} else if err != nil {
			return err
		}
		if quality != "" {
			if _, err = db.ExecContext(ctx, `DELETE FROM active_generations WHERE profile_id=? AND snapshot_id=?`, profile, snapshot); err != nil {
				return err
			}
			if _, err = db.ExecContext(ctx, `UPDATE integrity_generations SET quality=? WHERE snapshot_id=?`, quality, snapshot); err != nil {
				return err
			}
		}
	}
}
