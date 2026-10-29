package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// integrityWork is private durable control state, never an input filesystem path.
type integrityWork struct{ id, profile, snapshot, operation, stamp, binding, job string }

// startIntegrityWorker serializes expensive checks outside the manager lock and joins normal shutdown.
func (m *Manager) startIntegrityWorker() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.integrityStep(m.ctx)
			}
		}
	}()
}

// integrityStep reserves a reader before releasing the lifecycle lock and never guesses healthy on timeout.
func (m *Manager) integrityStep(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	// Reserve the observation connection before lifecycle ownership. Discovery
	// must not wait for the writer pool while blocking unrelated reader admission.
	conn, err := m.readDB.Conn(ctx)
	if err != nil {
		return
	}
	const discovery = `SELECT o.id,o.profile_id,o.snapshot_id,o.operation,o.stamp,o.binding,g.job_id FROM integrity_operations o JOIN integrity_generations g ON g.snapshot_id=o.snapshot_id WHERE o.state='queued' ORDER BY o.attempts,o.rowid LIMIT 1`
	var candidate integrityWork
	err = conn.QueryRowContext(ctx, discovery).Scan(&candidate.id, &candidate.profile, &candidate.snapshot, &candidate.operation, &candidate.stamp, &candidate.binding, &candidate.job)
	if err != nil {
		conn.Close()
		return
	}
	m.mu.Lock()
	if m.closed || ctx.Err() != nil {
		m.mu.Unlock()
		conn.Close()
		return
	}
	// Requalify all operation bindings under the existing lifecycle lock; no read
	// transaction or SQLite shared lock spans the mutex acquisition.
	var w integrityWork
	err = conn.QueryRowContext(ctx, discovery).Scan(&w.id, &w.profile, &w.snapshot, &w.operation, &w.stamp, &w.binding, &w.job)
	conn.Close()
	if err != nil || w != candidate {
		m.mu.Unlock()
		return
	}
	if w.operation == "delete" {
		_, _ = m.db.ExecContext(ctx, `UPDATE integrity_operations SET attempts=attempts+1 WHERE id=?`, w.id)
		err = m.removeIntegrityGeneration(ctx, w)
		if err == nil {
			tx, e := m.db.BeginTx(ctx, nil)
			if e == nil {
				defer tx.Rollback()
				_, e = tx.ExecContext(ctx, `UPDATE integrity_generations SET quality='deleted',checked_at=? WHERE snapshot_id=?`, now(), w.snapshot)
				if e == nil {
					_, e = tx.ExecContext(ctx, `UPDATE integrity_operations SET state='completed' WHERE id=?`, w.id)
				}
				if e == nil {
					_, e = tx.ExecContext(ctx, `UPDATE retention_qualification SET stamp='removed' WHERE snapshot_id=?`, w.snapshot)
				}
				if e == nil {
					if tx.Commit() == nil {
						m.integrityStates.Store(w.snapshot, integrityGuard{"deleted", w.stamp})
					}
				}
			}
		}
		m.mu.Unlock()
		return
	}
	if _, err = m.db.ExecContext(ctx, `UPDATE integrity_operations SET state='running',attempts=attempts+1 WHERE id=?`, w.id); err != nil {
		m.mu.Unlock()
		return
	}
	var outcomeRaw string
	err = m.db.QueryRowContext(ctx, `SELECT outcome_json FROM jobs WHERE id=?`, w.job).Scan(&outcomeRaw)
	m.readerMu.Lock()
	if m.generationReaders == nil {
		m.generationReaders = map[string]int{}
	}
	m.generationReaders[w.snapshot]++
	m.readerMu.Unlock()
	m.mu.Unlock()
	quality := "unverified"
	root, file, e := m.openRetentionFile(w.snapshot)
	if e == nil {
		info, x := file.Stat()
		if x == nil && retainedStamp(info) == w.stamp {
			db, x := retainedFileDB(file)
			if x == nil {
				if err == nil {
					err = index.VerifyGeneration(ctx, db, w.snapshot, w.profile, w.job)
				}
				if err == nil {
					var expected Outcome
					h, he := index.ReadGenerationHeader(ctx, db, w.snapshot)
					if json.Unmarshal([]byte(outcomeRaw), &expected) != nil || he != nil || expected.ScopeManifest == nil || !reflect.DeepEqual(h.Manifest, *expected.ScopeManifest) || !reflect.DeepEqual(h.Profile, expected.Job.CapturedProfile) {
						err = index.ErrGeneration
					}
				}
				db.Close()
				quality = integrityQuality(ctx, err)
			}
			current, x := root.Lstat("index.db")
			if x != nil || retainedStamp(current) != w.stamp {
				quality = "unverified"
			}
		}
		file.Close()
		root.Close()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() { m.readerMu.Lock(); m.generationReaders[w.snapshot]--; m.readerMu.Unlock() }()
	if m.closed {
		return
	}
	// Result publication is short and independent from an expired check budget.
	finish, done := context.WithTimeout(parent, time.Second)
	defer done()
	currentRoot, currentFile, e := m.openRetentionFile(w.snapshot)
	if e == nil {
		current, e := currentFile.Stat()
		if e != nil || retainedStamp(current) != w.stamp {
			quality = "unverified"
		}
		currentFile.Close()
		currentRoot.Close()
	} else {
		quality = "unverified"
	}
	tx, e := m.db.BeginTx(finish, nil)
	if e != nil {
		return
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(finish, `UPDATE integrity_generations SET quality=CASE WHEN ?='unverified' AND quality IN ('corrupt','unsupported') THEN quality ELSE ? END,checked_at=? WHERE snapshot_id=?`, quality, quality, now(), w.snapshot); e != nil {
		return
	}
	state := "completed"
	if quality == "unverified" {
		state = "failed"
	}
	if _, e = tx.ExecContext(finish, `UPDATE integrity_operations SET state=? WHERE id=?`, state, w.id); e != nil {
		return
	}
	if quality == "healthy" {
		_, e = tx.ExecContext(finish, `UPDATE retention_qualification SET stamp=? WHERE snapshot_id=?`, w.stamp, w.snapshot)
	}
	if e == nil {
		if tx.Commit() == nil {
			if quality == "unverified" {
				if old, ok := m.integrityStates.Load(w.snapshot); ok {
					prior := old.(integrityGuard).quality
					if prior == "corrupt" || prior == "unsupported" {
						quality = prior
					}
				}
			}
			m.integrityStates.Store(w.snapshot, integrityGuard{quality, w.stamp})
		}
	}
}

// integrityQuality distinguishes proven corruption from cancellation, unsupported versions and unknown failures.
func integrityQuality(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "unverified"
	}
	if err == nil {
		return "healthy"
	}
	if errors.Is(err, index.ErrUnsupportedGeneration) {
		return "unsupported"
	}
	if errors.Is(err, index.ErrGeneration) {
		return "corrupt"
	}
	var code interface{ Code() int }
	if errors.As(err, &code) && (code.Code()&255 == 11 || code.Code()&255 == 26) {
		return "corrupt"
	}
	return "unverified"
}

// removeIntegrityGeneration deletes only a confirmed exact package file and its empty bound directory.
// Durable binding allows restart after unlink; no recursive deletion, source path or profile removal is used.
func (m *Manager) removeIntegrityGeneration(ctx context.Context, w integrityWork) error {
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	if m.generationReaders[w.snapshot] > 0 || m.leaseSnapshotProtected(w.snapshot) {
		return ErrConflict
	}
	parent, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations"))
	if err != nil {
		return err
	}
	defer parent.Close()
	before, err := parent.Lstat(w.snapshot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !before.IsDir() {
		return ErrUnavailable
	}
	dir, binding, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", w.snapshot))
	if err != nil {
		return err
	}
	defer dir.Close()
	var expected scanner.RootBinding
	if json.Unmarshal([]byte(w.binding), &expected) != nil || expected != binding {
		return ErrUnavailable
	}
	held, err := dir.Stat(".")
	if err != nil || !os.SameFile(before, held) {
		return ErrUnavailable
	}
	list, err := dir.Open(".")
	if err != nil {
		return err
	}
	entries, err := list.ReadDir(3)
	list.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > 1 || (len(entries) == 1 && entries[0].Name() != "index.db") {
		return ErrUnavailable
	}
	info, err := dir.Lstat("index.db")
	if err == nil {
		if retainedStamp(info) != w.stamp {
			return ErrUnavailable
		}
		file, e := dir.Open("index.db")
		if e != nil {
			return e
		}
		defer file.Close()
		actual, e := file.Stat()
		if !ownIntegrityFile(file) || e != nil || !os.SameFile(info, actual) || retainedStamp(actual) != w.stamp {
			return ErrUnavailable
		}
		current, e := dir.Lstat("index.db")
		parentNow, e2 := parent.Lstat(w.snapshot)
		if e != nil || e2 != nil || retainedStamp(current) != w.stamp || !os.SameFile(held, parentNow) {
			return ErrUnavailable
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = dir.Remove("index.db"); e != nil {
			return e
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	current, err := parent.Lstat(w.snapshot)
	if err != nil || !os.SameFile(held, current) {
		return ErrUnavailable
	}
	if err = parent.Remove(w.snapshot); err != nil {
		return err
	}
	fd, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer fd.Close()
	return fd.Sync()
}

// ownIntegrityFile rejects hardlinks and foreign ownership before a destructive receipt is accepted.
func ownIntegrityFile(file *os.File) bool {
	var stat unix.Stat_t
	return unix.Fstat(int(file.Fd()), &stat) == nil && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1 && stat.Mode&unix.S_IFMT == unix.S_IFREG
}
