package index

import (
	"context"
	"database/sql"
)

// validateSQLite checks structural integrity and foreign keys before sealing a generation.
// SQLite emits at most one diagnostic; raw database details never become a public error.
func validateSQLite(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA integrity_check(1)`)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		var result string
		if err = rows.Scan(&result); err != nil {
			break
		}
		count++
		if result != "ok" {
			err = ErrGeneration
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrGeneration
	}
	rows, err = tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	invalid := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if invalid {
		return ErrGeneration
	}
	return nil
}
