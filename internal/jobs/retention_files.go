package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// retainedFileDB opens SQLite through the exact held regular file descriptor used for deletion qualification.
func retainedFileDB(file *os.File) (*sql.DB, error) {
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	u := url.URL{Scheme: "file", Path: prefix + strconv.FormatUint(uint64(file.Fd()), 10)}
	db, err := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// removeRetainedGeneration holds the same lease/refcount exclusion as normal readers for every destructive step.
func (m *Manager) removeRetainedGeneration(ctx context.Context, profile, snapshot, job, saved string) error {
	return m.removeRetainedGenerationChecked(ctx, profile, snapshot, job, saved, nil)
}

// The optional barrier exists only for deterministic inode/symlink replacement tests, never runtime configuration.
func (m *Manager) removeRetainedGenerationChecked(ctx context.Context, profile, snapshot, job, saved string, barrier func()) error {
	if !identifier.MatchString(profile) || !identifier.MatchString(snapshot) || !identifier.MatchString(job) {
		return ErrInvalid
	}
	if err := m.generationIntegrityAllowed(snapshot); err != nil {
		return err
	}
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	if m.generationReaders[snapshot] > 0 || m.leaseSnapshotProtected(snapshot) {
		return ErrConflict
	}
	parent, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations"))
	if err != nil {
		return err
	}
	defer parent.Close()
	before, err := parent.Lstat(snapshot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !before.IsDir() {
		return ErrUnavailable
	}
	dir, binding, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", snapshot))
	if err != nil {
		return err
	}
	defer dir.Close()
	dirInfo, err := dir.Stat(".")
	if err != nil || !os.SameFile(before, dirInfo) {
		return ErrUnavailable
	}
	if saved != "" {
		var prior scanner.RootBinding
		if json.Unmarshal([]byte(saved), &prior) != nil || prior != binding {
			return ErrUnavailable
		}
	}
	listing, err := dir.Open(".")
	if err != nil {
		return err
	}
	entries, err := listing.ReadDir(3)
	listing.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 1 {
		return ErrUnavailable
	}
	if len(entries) == 1 && entries[0].Name() != "index.db" {
		return ErrUnavailable
	}
	info, err := dir.Lstat("index.db")
	if err == nil {
		if !info.Mode().IsRegular() {
			return ErrUnavailable
		}
		file, e := dir.Open("index.db")
		if e != nil {
			return e
		}
		defer file.Close()
		held, e := file.Stat()
		if e != nil || !os.SameFile(info, held) {
			return ErrUnavailable
		}
		var evidence string
		if m.db.QueryRowContext(ctx, `SELECT stamp FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=?`, snapshot, profile, job).Scan(&evidence) != nil || evidence == "" || retainedStamp(held) != evidence {
			return ErrUnavailable
		}
		db, e := retainedFileDB(file)
		if e != nil {
			return e
		}
		h, e := index.ReadGenerationHeader(ctx, db, snapshot)
		db.Close()
		if e != nil || h.Profile.ID != profile || h.JobID != job || h.State != "complete" {
			return ErrUnavailable
		}
		encoded, _ := json.Marshal(binding)
		if _, e = m.db.ExecContext(ctx, `UPDATE retention_cleanup SET binding_json=? WHERE profile_id=? AND snapshot_id=?`, string(encoded), profile, snapshot); e != nil {
			return e
		}
		if barrier != nil {
			barrier()
		}
		current, e := dir.Lstat("index.db")
		parentNow, e2 := parent.Lstat(snapshot)
		if e != nil || e2 != nil || !current.Mode().IsRegular() || retainedStamp(held) != retainedStamp(current) || !os.SameFile(held, current) || !os.SameFile(dirInfo, parentNow) {
			return ErrUnavailable
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = dir.Remove("index.db"); e != nil {
			return e
		}
	} else if !errors.Is(err, os.ErrNotExist) || saved == "" {
		return ErrUnavailable
	}
	// Never recurse: unexpected files or a replaced parent remain untouched.
	current, err := parent.Lstat(snapshot)
	if err != nil || !os.SameFile(dirInfo, current) {
		return ErrUnavailable
	}
	if err = parent.Remove(snapshot); err != nil {
		return err
	}
	fd, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer fd.Close()
	return fd.Sync()
}
