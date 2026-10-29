package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// zeroContent represents a known empty set, independently of source coverage.
func zeroContent() contracts.ContentAggregate {
	z := contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}
	return contracts.ContentAggregate{FileCount: "0", DirectoryCount: "0", SpecialCount: "0", Logical: z, Allocated: z, Types: map[string]contracts.TypeAggregate{}}
}

// validContent bounds persisted vectors before arithmetic; sums are also checked by recurrence.
func validContent(c contracts.ContentAggregate) bool {
	values := []contracts.Bytes{c.FileCount, c.DirectoryCount, c.SpecialCount, c.Logical.KnownBytes, c.Logical.UnknownFileCount, c.Allocated.KnownBytes, c.Allocated.UnknownFileCount}
	if len(c.Types) > 8 {
		return false
	}
	for k, t := range c.Types {
		if !aggregateCategories[k] || t.FileCount == "0" {
			return false
		}
		values = append(values, t.FileCount, t.Logical.KnownBytes, t.Logical.UnknownFileCount, t.Allocated.KnownBytes, t.Allocated.UnknownFileCount)
	}
	for _, v := range values {
		if v.Validate() != nil || len(v) > 160 {
			return false
		}
	}
	return true
}

// directoryValue supports focused single-directory tests through the same bounded batch reader.
func directoryValue(ctx context.Context, tx *sql.Tx, id string, mapping map[string]string) (contracts.DirectoryAggregate, error) {
	values, err := directoryValues(ctx, tx, []string{id}, mapping)
	return values[id], err
}

// directoryValues streams children for at most 64 directories in one SQLite query.
// Build callers supply one depth at a time, so every child aggregate is already stored.
func directoryValues(ctx context.Context, tx *sql.Tx, ids []string, mapping map[string]string) (map[string]contracts.DirectoryAggregate, error) {
	if len(ids) == 0 || len(ids) > 64 {
		return nil, ErrGeneration
	}
	type totals struct{ direct, descendants contentAccumulator }
	sums := make(map[string]*totals, len(ids))
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		sums[id] = &totals{}
		args[i] = id
		marks[i] = "?"
	}
	rows, err := tx.QueryContext(ctx, `SELECT n.parent_id,n.kind,n.logical_bytes,n.allocated_bytes,n.name_bytes,a.aggregate_json FROM nodes n LEFT JOIN directory_aggregates a ON a.node_id=CASE WHEN n.kind='directory' THEN n.id END WHERE n.parent_id IN (`+strings.Join(marks, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Reuse SQL destinations while resetting optional node fields on every row.
	var parent string
	var n contracts.Node
	var name []byte
	var child, logical, allocated sql.NullString
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		n = contracts.Node{}
		if err = rows.Scan(&parent, &n.Kind, &logical, &allocated, &name, &child); err != nil {
			return nil, err
		}
		acc := sums[parent]
		if acc == nil {
			return nil, ErrGeneration
		}
		if logical.Valid {
			value := contracts.Bytes(logical.String)
			n.LogicalBytes = &value
		}
		if allocated.Valid {
			value := contracts.Bytes(allocated.String)
			n.AllocatedBytes = &value
		}
		if contracts.ValidateNodeType(n) != nil {
			return nil, ErrGeneration
		}
		switch n.Kind {
		case "directory":
			if n.LogicalBytes != nil || n.AllocatedBytes != nil || !child.Valid || len(child.String) > 32768 {
				return nil, ErrGeneration
			}
			var a contracts.DirectoryAggregate
			if json.Unmarshal([]byte(child.String), &a) != nil || !validContent(a.Direct) || !validContent(a.Subtree) {
				return nil, ErrGeneration
			}
			acc.direct.directories.Add(&acc.direct.directories, big.NewInt(1))
			acc.descendants.add(a.Subtree)
		case "file":
			if err = acc.direct.file(classifyName(name, mapping), n.LogicalBytes, n.AllocatedBytes); err != nil {
				return nil, err
			}
		default:
			acc.direct.special.Add(&acc.direct.special, big.NewInt(1))
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make(map[string]contracts.DirectoryAggregate, len(ids))
	for id, acc := range sums {
		direct := acc.direct.content()
		acc.descendants.add(direct)
		out[id] = contracts.DirectoryAggregate{Direct: direct, Subtree: acc.descendants.content()}
	}
	return out, nil
}

// buildAggregatesWithCache applies the qualified scan's bounded temporary-page cache.
func buildAggregatesWithCache(ctx context.Context, db *sql.DB, m contracts.FileTypeMapping, tempCacheKiB int) error {
	if tempCacheKiB != 1024 && tempCacheKiB != 2048 {
		return errors.New("INVALID_AGGREGATE_CACHE_BUDGET")
	}
	canonical, err := CanonicalFileTypeMapping(m)
	if err != nil {
		return err
	}
	// Temporary postorder state spills to disk with a fixed cache rather than scaling Go memory.
	if _, err = db.ExecContext(ctx, fmt.Sprintf(`PRAGMA temp_store=FILE; PRAGMA temp.cache_size=-%d`, tempCacheKiB)); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM directory_aggregates)+(SELECT count(*) FROM aggregate_mapping)`).Scan(&existing); err != nil {
		return err
	}
	if existing != 0 {
		return ErrGeneration
	}
	encoded, _ := json.Marshal(canonical)
	if _, err = tx.ExecContext(ctx, `INSERT INTO aggregate_mapping VALUES(1,?)`, string(encoded)); err != nil {
		return err
	}
	// Work rows live on disk and rank ascending is depth descending; pages never use OFFSET.
	if _, err = tx.ExecContext(ctx, `CREATE TEMP TABLE aggregate_order(rank INTEGER NOT NULL,node_id INTEGER NOT NULL,PRIMARY KEY(rank,node_id)) WITHOUT ROWID`); err != nil {
		return err
	}
	aggregateInsert, err := tx.PrepareContext(ctx, `INSERT INTO directory_aggregates VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer aggregateInsert.Close()
	// One bounded recursive SQL walk records only directory depth; no full paths are materialized.
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE dirs(id,depth) AS (SELECT id,0 FROM nodes WHERE parent_id IS NULL AND kind='directory' UNION ALL SELECT n.id,d.depth+1 FROM nodes n JOIN dirs d ON n.parent_id=d.id WHERE n.kind='directory' AND d.depth<256) INSERT INTO aggregate_order SELECT -depth,id FROM dirs`); err != nil {
		return err
	}
	lookup := map[string]string{}
	for _, a := range canonical.Assignments {
		lookup[a.Extension] = a.Category
	}
	rank, lastID := int64(-9223372036854775807), ""
	for {
		rows, e := tx.QueryContext(ctx, `SELECT rank,node_id FROM aggregate_order WHERE (rank,node_id)>(?,?) ORDER BY rank,node_id LIMIT 64`, rank, lastID)
		if e != nil {
			return e
		}
		type entry struct {
			rank int64
			id   string
		}
		batch := []entry{}
		for rows.Next() {
			var v entry
			if e = rows.Scan(&v.rank, &v.id); e != nil {
				break
			}
			batch = append(batch, v)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return e
		}
		if len(batch) == 0 {
			break
		}
		// Never combine ancestors and descendants in a build batch.
		sameDepth := 1
		for sameDepth < len(batch) && batch[sameDepth].rank == batch[0].rank {
			sameDepth++
		}
		batch = batch[:sameDepth]
		ids := make([]string, len(batch))
		for i, v := range batch {
			ids[i] = v.id
		}
		values, e := directoryValues(ctx, tx, ids, lookup)
		if e != nil {
			return e
		}
		for _, v := range batch {
			a := values[v.id]
			raw, e := json.Marshal(a)
			if e != nil {
				return e
			}
			if _, e = aggregateInsert.ExecContext(ctx, v.id, string(raw)); e != nil {
				return e
			}
			rank, lastID = v.rank, v.id
		}
	}
	if _, err = tx.ExecContext(ctx, `DROP TABLE aggregate_order`); err != nil {
		return err
	}
	return tx.Commit()
}

// validateAggregates independently verifies every stored recurrence before publication.
func validateAggregates(ctx context.Context, tx *sql.Tx, m contracts.FileTypeMapping) error {
	canonical, err := CanonicalFileTypeMapping(m)
	if err != nil {
		return err
	}
	rawWant, _ := json.Marshal(canonical)
	var actual string
	if err = tx.QueryRowContext(ctx, `SELECT mapping_json FROM aggregate_mapping WHERE singleton=1`).Scan(&actual); err != nil {
		return err
	}
	if actual != string(rawWant) {
		return ErrGeneration
	}
	var bad int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM directory_aggregates a LEFT JOIN nodes n ON n.id=a.node_id WHERE n.id IS NULL OR n.kind!='directory'`).Scan(&bad); err != nil {
		return err
	}
	if bad != 0 {
		return ErrGeneration
	}
	lookup := map[string]string{}
	for _, a := range canonical.Assignments {
		lookup[a.Extension] = a.Category
	}
	return walkDirectoryRows(ctx, tx, true, func(batch []directoryRow) error {
		for len(batch) > 0 {
			n := len(batch)
			if n > 64 {
				n = 64
			}
			part := batch[:n]
			batch = batch[n:]
			ids := make([]string, len(part))
			for i, v := range part {
				ids[i] = v.id
			}
			values, e := directoryValues(ctx, tx, ids, lookup)
			if e != nil {
				return e
			}
			for _, v := range part {
				var got contracts.DirectoryAggregate
				if !v.raw.Valid || len(v.raw.String) > 32768 || json.Unmarshal([]byte(v.raw.String), &got) != nil || !validContent(got.Direct) || !validContent(got.Subtree) {
					return ErrGeneration
				}
				if !reflect.DeepEqual(got, values[v.id]) {
					return ErrGeneration
				}
			}
		}
		return nil
	})
}

// directoryRow retains only one bounded page of directory metadata.
type directoryRow struct {
	id  string
	raw sql.NullString
}

// walkDirectoryRows filters inside SQLite, avoiding per-file decoding and empty Go pages.
// Keyset pagination bounds retained memory; QueryContext interrupts long file-only stretches.
func walkDirectoryRows(ctx context.Context, tx *sql.Tx, aggregates bool, visit func([]directoryRow) error) error {
	var cursor int64
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		projection := `SELECT n.id,NULL FROM nodes n`
		if aggregates {
			projection = `SELECT n.id,a.aggregate_json FROM nodes n LEFT JOIN directory_aggregates a ON a.node_id=n.id`
		}
		projection += ` WHERE n.kind='directory'`
		var args []any
		if !first {
			projection += ` AND n.id>?`
			args = append(args, cursor)
		}
		rows, err := tx.QueryContext(ctx, projection+` ORDER BY n.id LIMIT 64`, args...)
		if err != nil {
			return err
		}
		batch := make([]directoryRow, 0, 64)
		var v directoryRow
		for rows.Next() {
			if err = rows.Scan(&cursor, &v.raw); err != nil {
				break
			}
			v.id = strconv.FormatInt(cursor, 10)
			batch = append(batch, v)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = visit(batch); err != nil {
			return err
		}
		first = false
	}
}

// ReadDirectoryAggregate reads only stored generation data; source paths are never opened.
func ReadDirectoryAggregate(ctx context.Context, db *sql.DB, nodeID string) (contracts.DirectoryAggregate, error) {
	var out contracts.DirectoryAggregate
	var raw string
	err := db.QueryRowContext(ctx, `SELECT aggregate_json FROM directory_aggregates WHERE node_id=?`, nodeID).Scan(&raw)
	if err != nil {
		return out, err
	}
	if len(raw) > 32768 || json.Unmarshal([]byte(raw), &out) != nil || !validContent(out.Direct) || !validContent(out.Subtree) {
		return out, ErrGeneration
	}
	return out, nil
}
