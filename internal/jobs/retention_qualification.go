package jobs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// retainedStamp binds prior integrity evidence to the exact current regular file metadata identity.
func retainedStamp(info os.FileInfo) string {
	if !info.Mode().IsRegular() {
		return ""
	}
	return fmt.Sprintf("%s:%d:%d", scanner.ObjectIdentity(info), info.Size(), info.ModTime().UnixNano())
}

// openRetentionFile holds the qualified directory and file instead of reopening a mutable pathname for SQLite.
func (m *Manager) openRetentionFile(snapshot string) (*os.Root, *os.File, error) {
	if !identifier.MatchString(snapshot) {
		return nil, nil, ErrInvalid
	}
	root, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", snapshot))
	if err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat("index.db")
	if err != nil || !before.Mode().IsRegular() {
		root.Close()
		return nil, nil, ErrUnavailable
	}
	f, err := root.Open("index.db")
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		root.Close()
		return nil, nil, ErrUnavailable
	}
	return root, f, nil
}

// attestPublishedGeneration is called only after the existing seal/integrity/publish transaction succeeded.
// Startup never manufactures this evidence for old files.
func (m *Manager) attestPublishedGeneration(ctx context.Context, profile, snapshot, job string) error {
	_, _ = m.db.ExecContext(ctx, `INSERT OR IGNORE INTO retention_qualification(snapshot_id,profile_id,job_id,stamp,attempts) VALUES(?,?,?,'',0)`, snapshot, profile, job)
	root, file, err := m.openRetentionFile(snapshot)
	if err != nil {
		return err
	}
	defer root.Close()
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	db, err := retainedFileDB(file)
	if err != nil {
		return err
	}
	h, err := index.ReadGenerationHeader(ctx, db, snapshot)
	db.Close()
	if err != nil || h.Profile.ID != profile || h.JobID != job || h.State != "complete" {
		return ErrUnavailable
	}
	current, err := root.Lstat("index.db")
	if err != nil || retainedStamp(info) != retainedStamp(current) {
		return ErrUnavailable
	}
	_, err = m.db.ExecContext(ctx, `INSERT INTO retention_qualification(snapshot_id,profile_id,job_id,stamp,attempts) VALUES(?,?,?,?,0) ON CONFLICT(snapshot_id) DO UPDATE SET stamp=excluded.stamp`, snapshot, profile, job, retainedStamp(info))
	return err
}

// startRetentionWorker owns one cancellable qualification/cleanup loop, joined by Manager.Close.
func (m *Manager) startRetentionWorker() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		// A quiet interval follows each bounded round; slow work cannot accumulate ticker debt.
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-timer.C:
				m.qualifyRetentionStep(m.ctx)
				timer.Reset(time.Second)
			}
		}
	}()
}

// qualifyRetentionStep reserves one fair persisted candidate, then checks SQLite outside the lifecycle lock.
func (m *Manager) qualifyRetentionStep(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	// Discovery is worker-owned bookkeeping, not a deletion decision. Never hold
	// lifecycle ownership while waiting for the shared control connection.
	if ctx.Err() != nil {
		return
	}
	// Serialize only maintenance rounds; request readers never acquire this mutex.
	if !m.retentionWorkMu.TryLock() {
		return
	}
	defer m.retentionWorkMu.Unlock()
	if err := m.discoverRetention(ctx); err != nil {
		return
	}
	snapshot, profile, job, attempts, err := m.retentionCandidate(ctx, time.Now())
	if err != nil {
		return
	}
	if snapshot == "" {
		m.mu.Lock()
		if !m.closed && ctx.Err() == nil {
			m.retainGenerationRound(ctx, true)
		}
		m.mu.Unlock()
		return
	}
	// A failed persistence operation is not an acknowledged attempt.
	if _, err = m.db.ExecContext(ctx, `UPDATE retention_qualification SET attempts=attempts+1 WHERE snapshot_id=? AND profile_id=? AND job_id=? AND stamp=''`, snapshot, profile, job); err != nil {
		return
	}
	m.deferRetentionCandidate(snapshot, attempts, time.Now())
	// Revalidate shutdown after unlocked SQL before acquiring a cleanup pin.
	m.mu.Lock()
	if m.closed || ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	m.readerMu.Lock()
	if m.generationReaders == nil {
		m.generationReaders = map[string]int{}
	}
	m.generationReaders[snapshot]++
	m.readerMu.Unlock()
	m.mu.Unlock()
	stamp := ""
	unsupportedStamp := ""
	root, file, e := m.openRetentionFile(snapshot)
	if e == nil {
		info, x := file.Stat()
		if x == nil {
			db, x := retainedFileDB(file)
			if x == nil {
				var version int
				if db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version) == nil && version != index.GenerationSchema {
					unsupportedStamp = retainedStamp(info)
				}
				var aggregate int
				if version == index.GenerationSchema && db.QueryRowContext(ctx, `SELECT json_extract(header_json,'$.aggregateVersion') FROM generation_header WHERE singleton=1`).Scan(&aggregate) == nil && aggregate != index.AggregateVersion {
					unsupportedStamp = retainedStamp(info)
				}
				h, x := index.ReadGenerationHeader(ctx, db, snapshot)
				var check string
				if x == nil {
					x = db.QueryRowContext(ctx, `PRAGMA integrity_check(1)`).Scan(&check)
				}
				db.Close()
				current, x2 := root.Lstat("index.db")
				if x == nil && x2 == nil && check == "ok" && h.Profile.ID == profile && h.JobID == job && h.State == "complete" && retainedStamp(info) == retainedStamp(current) {
					stamp = retainedStamp(info)
				}
			}
		}
		file.Close()
		root.Close()
	}
	defer func() { m.readerMu.Lock(); m.generationReaders[snapshot]--; m.readerMu.Unlock() }()
	// The reservation protects this generation through attestation. Updating its
	// existing row creates no deletion authority and needs no lifecycle mutex.
	if ctx.Err() != nil {
		return
	}
	if stamp != "" || unsupportedStamp != "" {
		root, file, e := m.openRetentionFile(snapshot)
		if e == nil {
			info, e := file.Stat()
			file.Close()
			root.Close()
			if e == nil && (retainedStamp(info) == stamp || retainedStamp(info) == unsupportedStamp) {
				if unsupportedStamp != "" {
					stamp = "unsupported"
				}
				_, _ = m.db.ExecContext(ctx, `UPDATE retention_qualification SET stamp=? WHERE snapshot_id=? AND profile_id=? AND job_id=?`, stamp, snapshot, profile, job)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && ctx.Err() == nil {
		m.retainGenerationRound(ctx, true)
	}
}
