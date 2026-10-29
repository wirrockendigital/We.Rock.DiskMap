package jobs

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// MappingUpdate is a private optimistic configuration write, not a browser path API.
type MappingUpdate struct {
	ContractVersion  int                       `json:"contractVersion"`
	ExpectedRevision uint64                    `json:"expectedRevision"`
	Mapping          contracts.FileTypeMapping `json:"mapping"`
}

// initializeFileTypeMapping creates explicit initial settings without modifying old jobs.
func initializeFileTypeMapping(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS file_type_mapping(singleton INTEGER PRIMARY KEY CHECK(singleton=1),mapping_json TEXT NOT NULL)`); err != nil {
		return err
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM file_type_mapping`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		raw, _ := json.Marshal(index.DefaultFileTypeMapping())
		if _, err = tx.ExecContext(ctx, `INSERT INTO file_type_mapping VALUES(1,?)`, string(raw)); err != nil {
			return err
		}
	}
	if _, err = readFileTypeMapping(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// readFileTypeMapping returns an independently allocated, validated snapshot.
func readFileTypeMapping(ctx context.Context, tx *sql.Tx) (contracts.FileTypeMapping, error) {
	var raw string
	var m contracts.FileTypeMapping
	if err := tx.QueryRowContext(ctx, `SELECT mapping_json FROM file_type_mapping WHERE singleton=1`).Scan(&raw); err != nil {
		return m, err
	}
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &m) != nil {
		return m, ErrUnavailable
	}
	out, err := index.CanonicalFileTypeMapping(m)
	if err != nil {
		return m, ErrUnavailable
	}
	return out, nil
}

// GetFileTypeMapping exposes only settings, never source paths or credentials.
func (m *Manager) GetFileTypeMapping(ctx context.Context) (contracts.FileTypeMapping, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return contracts.FileTypeMapping{}, ErrUnavailable
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return contracts.FileTypeMapping{}, err
	}
	defer tx.Rollback()
	return readFileTypeMapping(ctx, tx)
}

// SetFileTypeMapping rejects concurrent scans so a joined job cannot imply a new mapping.
func (m *Manager) SetFileTypeMapping(ctx context.Context, in MappingUpdate) (contracts.FileTypeMapping, error) {
	var empty contracts.FileTypeMapping
	if in.ContractVersion != 1 || in.ExpectedRevision == 0 || in.Mapping.Revision != in.ExpectedRevision+1 {
		return empty, ErrInvalid
	}
	next, err := index.CanonicalFileTypeMapping(in.Mapping)
	if err != nil {
		return empty, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return empty, ErrUnavailable
	}
	if n, e := m.queueCount(); e != nil {
		return empty, e
	} else if m.active != nil || n > 0 {
		return empty, ErrConflict
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	current, err := readFileTypeMapping(ctx, tx)
	if err != nil {
		return empty, err
	}
	if current.Revision != in.ExpectedRevision {
		return empty, ErrConflict
	}
	raw, _ := json.Marshal(next)
	if _, err = tx.ExecContext(ctx, `UPDATE file_type_mapping SET mapping_json=? WHERE singleton=1`, string(raw)); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return next, nil
}
