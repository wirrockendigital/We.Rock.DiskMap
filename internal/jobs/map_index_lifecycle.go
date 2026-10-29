package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/mapindex"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// mapIndexBinding records only local generation identity, never source paths or credentials.
type mapIndexBinding struct {
	Snapshot string `json:"snapshot"`
	Key      string `json:"key"`
}

// startMapIndexWorker prepares existing active generations incrementally without rescanning sources.
func (m *Manager) startMapIndexWorker() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		cursor := ""
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-timer.C:
				m.mu.Lock()
				idle := !m.closed && m.active == nil
				m.mu.Unlock()
				if idle {
					ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
					var profile, snapshot string
					err := m.readDB.QueryRowContext(ctx, `SELECT p.id,COALESCE((SELECT json_extract(j.outcome_json,'$.job.resultSnapshotId') FROM jobs j WHERE json_extract(j.outcome_json,'$.job.profileId')=p.id AND json_extract(j.outcome_json,'$.job.resultSnapshotId') IS NOT NULL AND j.state='completed' ORDER BY j.rowid DESC LIMIT 1),a.snapshot_id,'') FROM profiles p LEFT JOIN active_generations a ON a.profile_id=p.id WHERE p.id>? ORDER BY p.id LIMIT 1`, cursor).Scan(&profile, &snapshot)
					if err == nil {
						cursor = profile
						db, release, e := m.generationDB(snapshot)
						if e == nil {
							key := m.querySourceKey(ctx, db, snapshot, "visual-v4")
							release()
							if key != "" {
								root, e := m.mapIndexDirectory()
								if e == nil {
									r, e := mapindex.Open(filepath.Join(root, key))
									if e == nil {
										r.Close()
									} else {
										_ = m.requestMapIndex(snapshot, key, false)
									}
								}
							}
						}
					} else {
						cursor = ""
					}
					cancel()
					m.cleanMapIndexes()
				}
				timer.Reset(5 * time.Second)
			}
		}
	}()
}

// cleanMapIndexes removes only known private derived files after their source generation is gone.
// Incomplete staging directories are reclaimed only when no builder owns one.
func (m *Manager) cleanMapIndexes() {
	m.mapIndexMu.Lock()
	defer m.mapIndexMu.Unlock()
	if m.mapIndexBusy {
		return
	}
	path, err := m.mapIndexDirectory()
	if err != nil {
		return
	}
	root, _, err := scanner.OpenBoundRoot(path)
	if err != nil {
		return
	}
	defer root.Close()
	listing, err := root.Open(".")
	if err != nil {
		return
	}
	defer listing.Close()
	// Retain only the next 64 names while streaming the directory in bounded batches.
	// A lexical cursor also advances past retained artifacts, unlike repeatedly reading its first page.
	entries := make([]os.DirEntry, 0, 64)
	for {
		batch, readErr := listing.ReadDir(64)
		for _, entry := range batch {
			if entry.Name() > m.mapIndexCleanAfter {
				entries = append(entries, entry)
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		if len(entries) > 64 {
			entries = entries[:64]
		}
		if readErr != nil {
			break
		}
	}
	if len(entries) == 0 {
		m.mapIndexCleanAfter = ""
		return
	}
	m.mapIndexCleanAfter = entries[len(entries)-1].Name()
	for _, entry := range entries {
		name := entry.Name()
		staging := strings.HasPrefix(name, ".prepare-")
		if !entry.IsDir() || !staging && !validQueryCacheKey(name) {
			continue
		}
		dir, e := root.OpenRoot(name)
		if e != nil {
			continue
		}
		held, e := dir.Open(".")
		if e != nil {
			dir.Close()
			continue
		}
		safe := privateFilterFile(held, true)
		held.Close()
		if !safe {
			dir.Close()
			continue
		}
		remove := staging
		if !staging {
			raw, e := dir.ReadFile("binding.json")
			var b mapIndexBinding
			if e == nil && len(raw) < 1024 && json.Unmarshal(raw, &b) == nil && b.Key == name && identifier.MatchString(b.Snapshot) {
				_, e = os.Lstat(filepath.Join(m.state, "generations", b.Snapshot))
				remove = os.IsNotExist(e)
			}
		}
		if remove {
			// No recursive deletion: unexpected directory contents make cleanup fail closed.
			for _, file := range []string{"tree.bin", "lookup.sqlite", "lookup.sqlite-journal", "balance.tmp", "packed.tmp", "addresses.tmp", "pack-work.tmp", "pack-queue.tmp", "lookup-packed.tmp", "lookup-packed.tmp-journal", "blocks.sha256", "blocks.gz", "offsets.bin", "binding.json"} {
				info, e := dir.Lstat(file)
				if e == nil && info.Mode().IsRegular() {
					_ = dir.Remove(file)
				}
			}
			_ = root.Remove(name)
		}
		dir.Close()
	}
}
