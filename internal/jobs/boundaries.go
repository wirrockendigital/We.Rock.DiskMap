package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// boundaryExamples has a shared response allowance; private original paths remain in SQLite.
func boundaryExamples(ctx context.Context, db *sql.DB) ([]contracts.ScanBoundary, error) {
	out := []contracts.ScanBoundary{}
	rows, err := db.QueryContext(ctx, `SELECT target_id,substr(path_bytes,1,768),length(path_bytes),reason_code FROM scan_boundaries ORDER BY target_id,path_bytes,reason_code LIMIT 32`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	size := 2
	for rows.Next() {
		var b contracts.ScanBoundary
		var raw []byte
		var length int
		if err := rows.Scan(&b.TargetID, &raw, &length, &b.ReasonCode); err != nil {
			return nil, err
		}
		if !index.ValidBoundaryReason(b.ReasonCode) {
			return nil, ErrUnavailable
		}
		b.RelativePath = string(raw)
		if !utf8.Valid(raw) {
			b.RelativePath = strconv.QuoteToASCII(string(raw))
		}
		if length > len(raw) {
			b.RelativePath += "…"
		}
		encoded, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		if size+len(encoded)+1 > 8*1024 {
			break
		}
		size += len(encoded) + 1
		out = append(out, b)
	}
	return out, rows.Err()
}
