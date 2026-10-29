package index

import (
	"context"
	"database/sql"
	"errors"
)

var ErrUnsupportedGeneration = errors.New("UNSUPPORTED_GENERATION_SCHEMA")

// VerifyGeneration checks the native sealed format without altering or migrating its bytes.
// Unsupported formats require a new scan; no unqualified conversion is attempted.
func VerifyGeneration(ctx context.Context, db *sql.DB, snapshot, profile, job string) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != GenerationSchema {
		return ErrUnsupportedGeneration
	}
	var aggregate int
	if err := db.QueryRowContext(ctx, `SELECT json_extract(header_json,'$.aggregateVersion') FROM generation_header WHERE singleton=1`).Scan(&aggregate); err != nil {
		return err
	}
	if aggregate != AggregateVersion {
		return ErrUnsupportedGeneration
	}
	h, err := ReadGenerationHeader(ctx, db, snapshot)
	if err != nil {
		return err
	}
	if h.Profile.ID != profile || h.JobID != job {
		return ErrGeneration
	}
	return validateGeneration(ctx, db, h, false)
}
