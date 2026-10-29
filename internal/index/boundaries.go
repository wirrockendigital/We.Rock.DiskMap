package index

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ValidBoundaryReason excludes arbitrary diagnostics from durable/public boundary records.
func ValidBoundaryReason(code string) bool {
	switch code {
	case "INDEX_RESET", "PACKAGE_STATE_EXCLUDED", "OUTSIDE_SCOPE_MOUNT", "SNAPSHOT_TREE_EXCLUDED", "SUBVOLUME_CLASSIFICATION_UNAVAILABLE", "SOURCE_UNREADABLE", "SOURCE_MISSING", "SOURCE_TRANSIENT", "SOURCE_CHANGED", "SOURCE_IO_ERROR", "SOURCE_LOCKED", "SOURCE_NOT_MOUNTED", "SOURCE_UNVERIFIED", "DEPTH_LIMIT", "PROFILE_EXCLUDED", "SCOPE_CHANGED", "SCOPE_VALIDATION_UNAVAILABLE":
		return true
	}
	return false
}

// RecordBoundary retains the exact private pathname without keeping a scan-sized list in RAM.
func RecordBoundary(ctx context.Context, tx *sql.Tx, target, relative, code string) error {
	if !security.ValidID(target) || security.RelativeBytes(relative) != nil || !ValidBoundaryReason(code) {
		return ErrGeneration
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO scan_boundaries VALUES(?,?,?)`, target, []byte(relative), code)
	return err
}

// validateBoundaries binds all records to captured roots and rejects malformed source paths.
func validateBoundaries(ctx context.Context, tx *sql.Tx, roots map[string]scopemanifest.ManifestRoot) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT b.target_id,b.path_bytes,b.reason_code,c.state,c.errors FROM scan_boundaries b LEFT JOIN coverage c ON c.target_id=b.target_id`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var count uint64
	for rows.Next() {
		var target, code string
		var relative []byte
		var state sql.NullString
		var gaps sql.NullInt64
		if err := rows.Scan(&target, &relative, &code, &state, &gaps); err != nil {
			return "", err
		}
		if _, ok := roots[target]; !ok || security.RelativeBytes(string(relative)) != nil || !ValidBoundaryReason(code) {
			return "", ErrGeneration
		}
		// Unclassified trees and foreign mounts cannot be sealed as complete root coverage.
		if BoundaryIsGap(code) && (!state.Valid || state.String != "partial" || !gaps.Valid || gaps.Int64 <= 0) {
			return "", ErrGeneration
		}
		count++
	}
	return strconv.FormatUint(count, 10), rows.Err()
}

// BoundaryIsGap distinguishes coverage failures from intentional exclusions.
func BoundaryIsGap(code string) bool {
	return ValidBoundaryReason(code) && code != "PACKAGE_STATE_EXCLUDED" && code != "SNAPSHOT_TREE_EXCLUDED" && code != "PROFILE_EXCLUDED"
}

// DirectorySizeUnmeasured distinguishes a measured empty tree from an unread or excluded tree.
// The primary-key range stays within one target and subtree; no source files are opened.
func DirectorySizeUnmeasured(ctx context.Context, db *sql.DB, target string, relative []byte) (bool, error) {
	var found int
	var err error
	if string(relative) == "." {
		err = db.QueryRowContext(ctx, `SELECT 1 FROM scan_boundaries WHERE target_id=? LIMIT 1`, target).Scan(&found)
	} else {
		lower := append(append([]byte{}, relative...), '/')
		upper := append(append([]byte{}, relative...), '0')
		err = db.QueryRowContext(ctx, `SELECT 1 FROM scan_boundaries WHERE target_id=? AND (path_bytes=? OR (path_bytes>=? AND path_bytes<?)) LIMIT 1`, target, relative, lower, upper).Scan(&found)
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return found == 1, err
}
