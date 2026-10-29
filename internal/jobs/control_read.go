package jobs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"os"
	"time"

	"modernc.org/sqlite"
	"net/url"
	"path/filepath"
)

// openControlReadPool isolates bounded observations from the single writer's
// connection queue. SQLite file locks and the original request deadlines still apply.
func openControlReadPool(ctx context.Context, state string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.Join(state, "control.db")}
	db, err := sql.Open("sqlite", uri.String()+"?mode=ro&_pragma=query_only(1)&_pragma=cache_size(-2048)&_pragma=temp_store(1)&_pragma=mmap_size(0)&_pragma=busy_timeout(100)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// openControlWriter bounds transient contention with the observation pool. The
// timeout applies to every replacement connection; FULL durability remains explicit.
func openControlWriter(ctx context.Context, state string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.Join(state, "control.db")}
	db, err := sql.Open("sqlite", uri.String()+"?_pragma=busy_timeout(100)&_pragma=synchronous(FULL)&_pragma=wal_autocheckpoint(256)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// retryControlRead retries only released, idempotent observations. Each attempt
// must close all rows, transactions, connections and reservations before returning.
// Failed attempt values are discarded; the caller's original deadline is never reset.
func retryControlRead[T any](ctx context.Context, attempt func() (T, error)) (T, error) {
	var zero T
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		out, err := attempt()
		if err == nil {
			return out, nil
		}
		var sqliteError *sqlite.Error
		if !errors.As(err, &sqliteError) || (sqliteError.Code()&255 != 5 && sqliteError.Code()&255 != 6) {
			return zero, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
}

// verifyControlSidecars admits only private crash-recovery files before SQLite can follow their names.
func verifyControlSidecars(state string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		file, err := security.OpenPrivateFile(filepath.Join(state, "control.db")+suffix, os.O_RDWR)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
	}
	return nil
}

// enableControlWAL lets readers observe committed state during durable preference and scan writes.
// Configure only a qualified schema; rejected upgrade inputs must remain byte-identical.
func enableControlWAL(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return err
	}
	if mode != "wal" {
		return ErrUnavailable
	}
	return nil
}

// checkpointControlForOffline restores the cold single-file boundary required by package upgrades and backups.
func checkpointControlForOffline(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var busy, log, checkpointed int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return ErrUnavailable
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		return err
	}
	if mode != "delete" {
		return ErrUnavailable
	}
	return nil
}
