package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// IndexResetRequest never authorizes source-file deletion; explicit confirmation is mandatory.
type IndexResetRequest struct {
	ContractVersion int              `json:"contractVersion"`
	All             bool             `json:"all"`
	Scope           *contracts.Scope `json:"scope"`
	Confirmed       bool             `json:"confirmed"`
}

func (r IndexResetRequest) Validate() error {
	if r.ContractVersion != 1 || !r.Confirmed || r.All == (r.Scope != nil) {
		return ErrInvalid
	}
	if r.All {
		return nil
	}
	s := r.Scope
	if !oneOf(s.Kind, "nas", "volumes", "shares", "folder") || s.Kind == "nas" && len(s.TargetIDs) != 0 || s.Kind != "nas" && len(s.TargetIDs) != 1 {
		return ErrInvalid
	}
	for _, id := range s.TargetIDs {
		if !security.ValidID(id) {
			return ErrInvalid
		}
	}
	if s.Kind == "folder" && !contracts.ValidFolderScope(*s) {
		return ErrInvalid
	}
	return nil
}

// IndexResetReceipt is recorded atomically with pointer changes for exact retry reconciliation.
type IndexResetReceipt struct {
	ResetID     string `json:"resetId"`
	Generations int    `json:"generations"`
	Completed   bool   `json:"completed"`
}

// resetAreas maps a live selector into every overlapping historical native scope.
func resetAreas(h index.GenerationHeader, s contracts.Scope) []index.ResetArea {
	out := []index.ResetArea{}
	for _, root := range h.Manifest.Provenance.Roots {
		if s.Kind == "nas" || s.Kind == "volumes" && root.VolumeID == s.TargetIDs[0] {
			out = append(out, index.ResetArea{Target: root.TargetID, Path: "."})
			continue
		}
		if s.Kind == "volumes" {
			continue
		}
		wanted := "."
		if s.Kind == "folder" {
			wanted = s.RelativePath
		}
		if root.Kind == "share" && root.SourceID != nil && *root.SourceID == s.TargetIDs[0] {
			prefix := "."
			for _, r := range h.Manifest.Roots {
				if r.TargetID == root.TargetID && r.RelativePath != "" {
					prefix = r.RelativePath
				}
			}
			switch {
			case wanted == "." || wanted == prefix || strings.HasPrefix(prefix, wanted+"/"):
				out = append(out, index.ResetArea{Target: root.TargetID, Path: "."})
			case prefix == ".":
				out = append(out, index.ResetArea{Target: root.TargetID, Path: wanted})
			case strings.HasPrefix(wanted, prefix+"/"):
				out = append(out, index.ResetArea{Target: root.TargetID, Path: strings.TrimPrefix(wanted, prefix+"/")})
			}
		}
		for _, a := range root.Areas {
			if a.SourceID != nil && *a.SourceID == s.TargetIDs[0] && a.Qualification == "qualified" {
				p := string(a.RelativePath)
				if wanted != "." {
					p += "/" + wanted
				}
				out = append(out, index.ResetArea{Target: root.TargetID, Path: p})
			}
		}
	}
	return out
}

// ResetIndex builds replacement generations first, then atomically switches every historical pointer.
// Active/queued scans reject the operation, preventing a late publication from resurrecting removed data.
func (m *Manager) ResetIndex(ctx context.Context, in IndexResetRequest, principal, key string) (IndexResetReceipt, error) {
	var out IndexResetReceipt
	if in.Validate() != nil || !validPrincipal(principal) {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return out, ErrUnavailable
	}
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{Principal: principal, Operation: "index.reset", Key: key, Payload: string(raw)}
	if replay.Validate() != nil {
		return out, ErrInvalid
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	prior, found, err := LookupReplay(tx, replay)
	tx.Rollback()
	if err != nil {
		return out, err
	}
	if found {
		err = json.Unmarshal(prior, &out)
		return out, err
	}
	var active int
	if err = m.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE `+liveStatusPredicate).Scan(&active); err != nil {
		return out, err
	}
	if active > 0 {
		return out, ErrProfileActive
	}
	rows, err := m.db.QueryContext(ctx, `SELECT outcome_json FROM jobs WHERE json_extract(outcome_json,'$.job.resultSnapshotId') IS NOT NULL`)
	if err != nil {
		return out, err
	}
	outcomes := []Outcome{}
	for rows.Next() {
		var raw string
		var o Outcome
		if err = rows.Scan(&raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &o); err != nil {
			break
		}
		outcomes = append(outcomes, o)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return out, err
	}
	type replacement struct {
		old     string
		outcome Outcome
	}
	changed := []replacement{}
	for _, o := range outcomes {
		old := *o.Job.ResultSnapshotID
		db, close, e := m.generationDB(old)
		if e != nil {
			// Retention may already have removed a historical generation; retire its stale pointer.
			if _, missing := os.Lstat(filepath.Join(m.state, "generations", old)); errors.Is(missing, os.ErrNotExist) {
				o.Job.ResultSnapshotID = nil
				o.SnapshotState = ""
				o.SnapshotEndedAt = nil
				changed = append(changed, replacement{old, o})
				continue
			}
			return out, e
		}
		h, e := index.ReadGenerationHeader(ctx, db, old)
		if e != nil {
			close()
			return out, e
		}
		areas := []index.ResetArea{}
		if !in.All {
			areas = resetAreas(h, *in.Scope)
		}
		if !in.All && len(areas) == 0 {
			close()
			continue
		}
		if in.All {
			o.Job.ResultSnapshotID = nil
			o.SnapshotState = ""
			o.SnapshotEndedAt = nil
			close()
		} else {
			snapshot, e := profiles.NewID()
			if e != nil {
				close()
				return out, e
			}
			dir := filepath.Join(m.state, "generations", snapshot)
			if e = os.Mkdir(dir, 0700); e != nil {
				close()
				return out, e
			}
			file := filepath.Join(dir, "index.db")
			var pages, pageSize uint64
			if e = db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pages); e != nil {
				close()
				return out, e
			}
			if e = db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); e != nil {
				close()
				return out, e
			}
			free, e := m.availableStateBytes()
			if e != nil || free <= IndexReserveBytes || pageSize == 0 || pages > (free-IndexReserveBytes)/pageSize {
				close()
				return out, ErrUnavailable
			}
			// This private connection remains mode=ro; VACUUM writes only the new destination.
			_, e = db.ExecContext(ctx, `PRAGMA query_only=OFF; VACUUM INTO ?`, file)
			close()
			if e != nil {
				return out, e
			}
			if e = os.Chmod(file, 0600); e != nil {
				return out, e
			}
			copy, e := index.Open(file)
			if e != nil {
				return out, e
			}
			next, e := index.ResetCopiedGeneration(ctx, copy, h, snapshot, areas)
			closeErr := copy.Close()
			if e != nil {
				return out, e
			}
			if closeErr != nil {
				return out, closeErr
			}
			// Publish pointers only after SQLite data and directory entries are durable.
			for _, path := range []string{file, dir, filepath.Dir(dir)} {
				f, e := os.Open(path)
				if e != nil {
					return out, e
				}
				e = errors.Join(f.Sync(), f.Close())
				if e != nil {
					return out, e
				}
			}
			o.Job.ResultSnapshotID = &snapshot
			o.SnapshotState = "partial"
			o.CoverageErrors = next.CoverageErrors
		}
		changed = append(changed, replacement{old, o})
	}
	// Cache readers/builders must finish before their generations can be invalidated.
	// Keeping this lock through publication prevents an already admitted old reader from recreating a cache.
	if cache := m.filterCache; cache != nil {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.building != nil {
			return out, ErrUnavailable
		}
		for _, entry := range cache.entries {
			if entry.refs != 0 {
				return out, ErrUnavailable
			}
		}
		for _, entry := range cache.entries {
			for _, replacement := range changed {
				if entry.header.SnapshotID == replacement.old {
					if err = cache.evict(entry); err != nil {
						return out, err
					}
					break
				}
			}
		}
	}
	tx, err = m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, v := range changed {
		encoded, _ := json.Marshal(v.outcome)
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET outcome_json=? WHERE id=?`, string(encoded), v.outcome.Job.ID); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM active_generations WHERE snapshot_id=?`, v.old); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO profile_cleanup(profile_id,snapshot_id,job_id) VALUES(?,?,?)`, v.outcome.Job.ProfileID, v.old, v.outcome.Job.ID); err != nil {
			return out, err
		}
	}
	out.ResetID, err = profiles.NewID()
	if err != nil {
		return out, err
	}
	out.Generations = len(changed)
	out.Completed = true
	encoded, _ := json.Marshal(out)
	if err = RecordReplay(tx, replay, encoded); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	if cache := m.filterCache; cache != nil {
		if cache.invalidated == nil {
			cache.invalidated = map[string]bool{}
		}
		for _, replacement := range changed {
			cache.invalidated[replacement.old] = true
		}
	}
	m.readerMu.Lock()
	for _, lease := range m.viewLeases {
		for _, v := range changed {
			if lease.Snapshot == v.old {
				lease.Released = true
			}
		}
	}
	m.readerMu.Unlock()
	m.cleanupProfiles(ctx)
	return out, nil
}
