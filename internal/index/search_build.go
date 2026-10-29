package index

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// BuildDerivedProjections builds only directory totals; search has no persistent projection.
func BuildDerivedProjections(ctx context.Context, db *sql.DB, mapping contracts.FileTypeMapping) error {
	return BuildDerivedProjectionsWithCache(ctx, db, mapping, 2048)
}

// BuildDerivedProjectionsWithCache preserves the scan's bounded SQLite memory policy.
func BuildDerivedProjectionsWithCache(ctx context.Context, db *sql.DB, mapping contracts.FileTypeMapping, cache int) error {
	return buildAggregatesWithCache(ctx, db, mapping, cache)
}
