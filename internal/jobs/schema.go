package jobs

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// initializeControl is used only for a new database or an isolated upgrade candidate.
func initializeControl(ctx context.Context, db *sql.DB) error {
	var before int
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&before) != nil {
		return ErrUnavailable
	}
	if before != 0 && before != 2 && before != 3 && before != 4 && before != 5 && before != 6 && before != migration.ControlVersion {
		return migration.ErrVersion
	}
	if before == 3 {
		if err := migratePersonalPreferences(ctx, db); err != nil {
			return err
		}
		if err := migrateExclusionTemplate(ctx, db); err != nil {
			return err
		}
		if err := migrateOnboarding(ctx, db); err != nil {
			return err
		}
		return migratePreferenceDisplay(ctx, db)
	}
	if before == 4 {
		if err := migrateExclusionTemplate(ctx, db); err != nil {
			return err
		}
		if err := migrateOnboarding(ctx, db); err != nil {
			return err
		}
		return migratePreferenceDisplay(ctx, db)
	}
	if before == 5 {
		if err := migrateOnboarding(ctx, db); err != nil {
			return err
		}
		return migratePreferenceDisplay(ctx, db)
	}
	if before == 6 {
		return migratePreferenceDisplay(ctx, db)
	}
	if before == migration.ControlVersion {
		return validateControl(ctx, db)
	}
	_, err := db.Exec(`PRAGMA synchronous=FULL; CREATE TABLE IF NOT EXISTS profiles(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,profile_json TEXT NOT NULL,roots_json TEXT NOT NULL,volumes_json TEXT NOT NULL); CREATE TABLE IF NOT EXISTS jobs(id TEXT PRIMARY KEY,state TEXT NOT NULL,outcome_json TEXT NOT NULL); CREATE TABLE IF NOT EXISTS idempotency(principal TEXT NOT NULL,operation TEXT NOT NULL,key TEXT NOT NULL,payload TEXT NOT NULL,result_json TEXT NOT NULL,PRIMARY KEY(principal,operation,key)); CREATE TABLE IF NOT EXISTS profile_sources(profile_id TEXT PRIMARY KEY,sources_json TEXT NOT NULL); CREATE TABLE IF NOT EXISTS active_generations(profile_id TEXT PRIMARY KEY,snapshot_id TEXT NOT NULL);`)
	if err != nil {
		return err
	}
	if err = initializeFileTypeMapping(ctx, db); err != nil {
		return err
	}
	if err = profiles.Initialize(ctx, db); err != nil {
		return err
	}
	if err = initializeProfileCleanup(ctx, db); err != nil {
		return err
	}
	if err = initializeJobPhases(ctx, db); err != nil {
		return err
	}
	if err = initializeRetention(ctx, db); err != nil {
		return err
	}
	// Integrity observations have their own durable state and never mutate sealed indexes.
	if err = initializeIntegrity(ctx, db); err != nil {
		return err
	}
	if err = initializeJobControls(ctx, db); err != nil {
		return err
	}
	// Progress is stored independently from immutable job and replay provenance.
	if err = initializeProgress(ctx, db); err != nil {
		return err
	}
	if err = initializeQueue(ctx, db); err != nil {
		return err
	}
	if err = migrateReplaySchema(db); err != nil {
		return err
	}

	_, err = db.ExecContext(ctx, `CREATE TABLE schema_steps(version INTEGER PRIMARY KEY, name TEXT NOT NULL); INSERT INTO schema_steps VALUES(3,'control-baseline-2026-10'); PRAGMA user_version=3`)
	if err != nil {
		return err
	}
	if err := migratePersonalPreferences(ctx, db); err != nil {
		return err
	}
	if err := migrateExclusionTemplate(ctx, db); err != nil {
		return err
	}
	if err := migrateOnboarding(ctx, db); err != nil {
		return err
	}
	return migratePreferenceDisplay(ctx, db)
}

// validateControl requires the supported explicit schema marker without opportunistic CREATE statements.
func validateControl(ctx context.Context, db *sql.DB) error {
	var version int
	var name string
	if db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil || version != migration.ControlVersion {
		return migration.ErrVersion
	}
	if db.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=3").Scan(&name) != nil || name != "control-baseline-2026-10" {
		return migration.ErrVersion
	}
	if db.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=4").Scan(&name) != nil || name != "personal-preferences-v1" {
		return migration.ErrVersion
	}
	if db.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=7").Scan(&name) != nil || name != "personal-display-v2" {
		return migration.ErrVersion
	}
	rows, err := db.QueryContext(ctx, "SELECT principal,revision,value_json FROM user_preferences LIMIT 0")
	if err != nil {
		return migration.ErrVersion
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if db.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=5").Scan(&name) != nil || name != "exclusion-template-v1" {
		return migration.ErrVersion
	}
	if _, err = readExclusionTemplate(ctx, db); err != nil {
		return err
	}
	if db.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=6").Scan(&name) != nil || name != "onboarding-draft-v1" {
		return migration.ErrVersion
	}
	rows, err = db.QueryContext(ctx, "SELECT principal,revision,value_json FROM onboarding_drafts LIMIT 0")
	if err != nil {
		return migration.ErrVersion
	}
	return rows.Close()
}

// Only a genuinely empty SQLite database can be initialized during ordinary service startup.
func validateOrInitializeControl(ctx context.Context, db *sql.DB) error {
	var tables, version int
	if db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'").Scan(&tables) != nil || db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		return ErrUnavailable
	}
	if tables == 0 && version == 0 {
		return initializeControl(ctx, db)
	}
	return validateControl(ctx, db)
}

// Migration changes only an offline candidate; transaction failure retains the v3 schema.
func migratePersonalPreferences(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var baseline string
	if tx.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=3").Scan(&baseline) != nil || baseline != "control-baseline-2026-10" {
		return migration.ErrVersion
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS user_preferences(principal TEXT PRIMARY KEY,revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 2147483647),value_json TEXT NOT NULL CHECK(length(CAST(value_json AS BLOB))<=16384)); INSERT INTO schema_steps VALUES(4,'personal-preferences-v1'); PRAGMA user_version=4`)
	if err != nil {
		return err
	}
	return tx.Commit()
}
