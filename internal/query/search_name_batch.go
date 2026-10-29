package query

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// searchReadBatch avoids reconstructing ancestors and public metadata for names that
// cannot match. Scope and all remaining predicates still qualify reconstructed hits.
// Progress and exhaustion follow physical rows, never the sparse matching subset.
func searchReadBatch(ctx context.Context, db *sql.DB, reader *index.NodeReader, r SearchRequest, needle string, after int64) ([]index.NodeRecord, int64, bool, error) {
	if r.Field != "name" || r.Text == "" {
		batch, err := reader.Page(after, 128)
		if len(batch) > 0 {
			after = batch[len(batch)-1].RowID
		}
		return batch, after, len(batch) < 128, err
	}
	start := after
	rows, err := db.QueryContext(ctx, `SELECT id,name_bytes FROM nodes WHERE id>? ORDER BY id LIMIT 128`, after)
	if err != nil {
		return nil, after, false, err
	}
	ids := make([]int64, 0, 128)
	count := 0
	for rows.Next() {
		var id int64
		var name []byte
		if err = rows.Scan(&id, &name); err != nil {
			rows.Close()
			return nil, after, false, err
		}
		count++
		after = id
		if searchTextMatches(name, needle, r.MatchMode) {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, after, false, err
	}
	if closeErr != nil {
		return nil, after, false, closeErr
	}
	// Dense name pages use the existing batch reader rather than one SQL lookup per hit.
	// Returning nonmatches here is safe because the caller applies the same literal predicate.
	if len(ids) > count/2 {
		batch, e := reader.Page(start, 128)
		return batch, after, count < 128, e
	}
	// Release the batch cursor before ancestry reads so a single leased connection suffices.
	batch := make([]index.NodeRecord, 0, len(ids))
	for _, id := range ids {
		record, e := reader.Read(strconv.FormatInt(id, 10))
		if e != nil {
			return nil, after, false, e
		}
		batch = append(batch, record)
	}
	return batch, after, count < 128, nil
}
