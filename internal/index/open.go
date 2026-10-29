// Package index owns local SQLite access and immutable generation storage.
package index

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Open connects to a caller-owned database; source files are never passed here.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open index: %w", err)
	}
	return db, nil
}
