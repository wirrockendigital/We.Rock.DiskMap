package index

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"math/big"
)

// ResetArea identifies an already resolved location inside a stored generation, never a live path.
type ResetArea struct{ Target, Path string }

// ResetCopiedGeneration changes only a private, unpublished SQLite copy and reseals it under a new identity.
func ResetCopiedGeneration(ctx context.Context, db *sql.DB, h GenerationHeader, snapshot string, areas []ResetArea) (GenerationHeader, error) {
	fail := func(err error) (GenerationHeader, error) { return GenerationHeader{}, err }
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	// Only the copied header needs a temporary seal exception; original generation files stay immutable.
	if _, err = tx.ExecContext(ctx, `DROP TRIGGER seal_header_DELETE; DELETE FROM generation_header;
 CREATE TRIGGER seal_header_DELETE BEFORE DELETE ON generation_header BEGIN SELECT RAISE(ABORT,'GENERATION_SEALED'); END;
 UPDATE generation_identity SET snapshot_id=?`, snapshot); err != nil {
		return fail(err)
	}
	for _, area := range areas {
		n, e := findNode(ctx, tx, area.Target, []byte(area.Path))
		if IsNodeNotFound(e) {
			continue
		}
		if e != nil {
			return fail(e)
		}
		// Keep the directory locator as an explicitly unknown boundary, removing all descendants atomically.
		if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE reset_nodes(id INTEGER PRIMARY KEY);
 INSERT INTO reset_nodes WITH RECURSIVE descendants(id) AS (SELECT id FROM nodes WHERE parent_id=? UNION ALL SELECT n.id FROM nodes n JOIN descendants d ON n.parent_id=d.id) SELECT id FROM descendants;
 DELETE FROM directory_aggregates WHERE node_id IN reset_nodes;
 DELETE FROM nodes WHERE id IN reset_nodes;
 DROP TABLE reset_nodes;`, n.RowID); err != nil {
			return fail(err)
		}
		if err = RecordBoundary(ctx, tx, area.Target, area.Path, "INDEX_RESET"); err != nil {
			return fail(err)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE coverage SET state='partial',errors=errors+1 WHERE target_id=?`, area.Target); err != nil {
			return fail(err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping`); err != nil {
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	h.SnapshotID = snapshot
	h.State = "partial"
	h.CoverageErrors += uint64(len(areas))
	h.Entries = 0
	sum := new(big.Int)
	rows, err := db.QueryContext(ctx, `SELECT logical_bytes FROM nodes`)
	if err != nil {
		return fail(err)
	}
	for rows.Next() {
		var v sql.NullString
		if err = rows.Scan(&v); err != nil {
			break
		}
		h.Entries++
		if v.Valid {
			n, ok := new(big.Int).SetString(v.String, 10)
			if !ok {
				err = ErrGeneration
				break
			}
			sum.Add(sum, n)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return fail(err)
	}
	h.LogicalBytes = contracts.Bytes(sum.String())
	if err = BuildDerivedProjectionsWithCache(ctx, db, h.FileTypeMapping, 2048); err != nil {
		return fail(err)
	}
	if err = FinalizeGeneration(ctx, db, h); err != nil {
		return fail(err)
	}
	// Finalize derives authoritative boundary counts and per-target coverage before sealing.
	result, err := ReadGenerationHeader(ctx, db, snapshot)
	return result, err
}
