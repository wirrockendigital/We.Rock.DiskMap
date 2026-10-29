package index

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
)

// ConfigureScan bounds SQLite page caches before any source traversal or index transaction.
// These connection budgets are not a claim about total process RSS or physical I/O bandwidth.
func ConfigureScan(ctx context.Context, db *sql.DB, policy resources.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA cache_size=-%d; PRAGMA temp_store=FILE; PRAGMA temp.cache_size=-%d; PRAGMA mmap_size=0", policy.SQLiteCacheKiB, policy.TempCacheKiB))
	return err
}
