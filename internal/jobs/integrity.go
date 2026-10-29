package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// IntegrityRequest contains only package identities; source paths are never accepted.
type IntegrityRequest struct {
	ContractVersion  int     `json:"contractVersion"`
	Operation        string  `json:"operation"`
	ProfileID        string  `json:"profileId"`
	SnapshotID       string  `json:"snapshotId"`
	ConfirmationName *string `json:"confirmationName,omitempty"`
}

// Validate makes destructive confirmation mandatory and prevents ignored fields on checks.
func (r IntegrityRequest) Validate() error {
	if r.ContractVersion != 1 || !identifier.MatchString(r.ProfileID) || !identifier.MatchString(r.SnapshotID) {
		return ErrInvalid
	}
	switch r.Operation {
	case "check":
		if r.ConfirmationName != nil {
			return ErrInvalid
		}
	case "delete":
		if r.ConfirmationName == nil || len(*r.ConfirmationName) > 512 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// IntegrityReceipt is immutable; progress is obtained separately from IntegrityStatus.
type IntegrityReceipt struct {
	OperationID string `json:"operationId"`
	Operation   string `json:"operation"`
	ProfileID   string `json:"profileId"`
	SnapshotID  string `json:"snapshotId"`
	State       string `json:"state"`
}
type IntegrityEntry struct {
	SnapshotID     string  `json:"snapshotId"`
	Quality        string  `json:"quality"`
	OperationState string  `json:"operationState"`
	CheckedAt      *string `json:"checkedAt"`
	Deletable      bool    `json:"deletable"`
}
type IntegrityStatus struct {
	ProfileID       string           `json:"profileId"`
	Generations     []IntegrityEntry `json:"generations"`
	Truncated       bool             `json:"truncated"`
	SupportedSchema int              `json:"supportedSchema"`
}

// initializeIntegrity only adds private control state; sealed generations remain unchanged.
func initializeIntegrity(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS integrity_generations(snapshot_id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,job_id TEXT NOT NULL,quality TEXT NOT NULL,stamp TEXT NOT NULL,owned INTEGER NOT NULL,binding TEXT NOT NULL,checked_at TEXT);
 CREATE TABLE IF NOT EXISTS integrity_operations(id TEXT PRIMARY KEY,profile_id TEXT NOT NULL,snapshot_id TEXT NOT NULL,operation TEXT NOT NULL,state TEXT NOT NULL,stamp TEXT NOT NULL,binding TEXT NOT NULL,attempts INTEGER NOT NULL DEFAULT 0);
 UPDATE integrity_operations SET state='queued' WHERE state='running';`)
	return err
}

// integrityGuard is an immutable copy published after a committed control transaction.
type integrityGuard struct{ quality, stamp string }

// loadIntegrityState restores reader guards before the manager is exposed; it never changes generations.
func (m *Manager) loadIntegrityState(ctx context.Context) error {
	rows, err := m.db.QueryContext(ctx, `SELECT snapshot_id,quality,stamp FROM integrity_generations`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, q, stamp string
		if err = rows.Scan(&id, &q, &stamp); err != nil {
			return err
		}
		m.integrityStates.Store(id, integrityGuard{q, stamp})
	}
	return rows.Err()
}

// generationIntegrityAllowed requires no SQL/locks because readers can be inside an owner transaction.
func (m *Manager) generationIntegrityAllowed(snapshot string) error {
	value, found := m.integrityStates.Load(snapshot)
	if !found {
		return nil
	}
	q := value.(integrityGuard).quality
	if q != "healthy" {
		return ErrUnavailable
	}
	return nil
}

// generationIntegrityFileAllowed binds known health to the actual held reader file, never a reopened path.
func (m *Manager) generationIntegrityFileAllowed(snapshot string, file *os.File) error {
	if err := m.generationIntegrityAllowed(snapshot); err != nil {
		return err
	}
	value, found := m.integrityStates.Load(snapshot)
	if !found {
		return nil
	}
	g := value.(integrityGuard)
	if g.quality == "healthy" {
		info, err := file.Stat()
		if err != nil || retainedStamp(info) != g.stamp {
			return ErrUnavailable
		}
	}
	return nil
}

// sameFileObject compares only device/inode from prior qualified evidence; ctime changes invalidate health.
func sameFileObject(a, b string) bool {
	x, y := strings.Split(a, ":"), strings.Split(b, ":")
	return len(x) == 6 && len(y) == 6 && x[0] == y[0] && x[1] == y[1]
}

// IntegrityAction commits an exact replay receipt and durable intent before background work begins.
func (m *Manager) IntegrityAction(parent context.Context, in IntegrityRequest, principal, key string) (out IntegrityReceipt, err error) {
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if m.closed {
		return out, ErrUnavailable
	}
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{principal, "index." + in.Operation, key, string(raw)}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	old, found, err := LookupReplay(tx, replay)
	if err != nil {
		return out, err
	}
	if found {
		err = json.Unmarshal(old, &out)
		return out, err
	}
	p, err := profiles.Read(ctx, tx, in.ProfileID)
	if err != nil {
		return out, ErrProfileMissing
	}
	var job string
	err = tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE state='completed' AND json_extract(outcome_json,'$.job.profileId')=? AND json_extract(outcome_json,'$.job.resultSnapshotId')=?`, in.ProfileID, in.SnapshotID).Scan(&job)
	if err != nil {
		return out, ErrNotFound
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM integrity_operations WHERE snapshot_id=? AND state IN ('queued','running')`, in.SnapshotID).Scan(&active); err != nil {
		return out, err
	}
	if active > 0 {
		return out, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM integrity_operations WHERE state IN ('queued','running')`).Scan(&active); err != nil {
		return out, err
	}
	if active >= 16 {
		return out, ErrQueueFull
	}
	root, file, err := m.openRetentionFile(in.SnapshotID)
	if err != nil {
		return out, ErrUnavailable
	}
	defer root.Close()
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return out, ErrUnavailable
	}
	stamp := retainedStamp(info)
	bound, binding, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", in.SnapshotID))
	if err != nil {
		return out, ErrUnavailable
	}
	heldDir, heldErr := root.Stat(".")
	boundDir, boundErr := bound.Stat(".")
	bound.Close()
	if heldErr != nil || boundErr != nil || !os.SameFile(heldDir, boundDir) {
		return out, ErrUnavailable
	}
	bindingRaw, _ := json.Marshal(binding)
	// A previously sealed identity or a currently bound native header proves package ownership.
	owned := 0
	var prior string
	_ = tx.QueryRowContext(ctx, `SELECT stamp FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=?`, in.SnapshotID, in.ProfileID, job).Scan(&prior)
	if sameFileObject(prior, stamp) {
		owned = 1
	}
	var priorOwned int
	var priorStamp string
	_ = tx.QueryRowContext(ctx, `SELECT owned,stamp FROM integrity_generations WHERE snapshot_id=? AND profile_id=?`, in.SnapshotID, in.ProfileID).Scan(&priorOwned, &priorStamp)
	if priorOwned == 1 && sameFileObject(priorStamp, stamp) {
		owned = 1
	}
	db, e := retainedFileDB(file)
	if e == nil {
		h, e := index.ReadGenerationHeader(ctx, db, in.SnapshotID)
		db.Close()
		if e == nil && h.Profile.ID == in.ProfileID && h.JobID == job {
			owned = 1
		}
	}
	quality := "unverified"
	if in.Operation == "delete" {
		if *in.ConfirmationName != p.Profile.Name {
			return out, ErrInvalid
		}
		if owned != 1 || !ownIntegrityFile(file) {
			return out, ErrUnavailable
		}
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=? AND state NOT IN ('completed','failed','cancelled','interrupted')`, in.ProfileID).Scan(&active); err != nil {
			return out, err
		}
		if active > 0 {
			return out, ErrProfileActive
		}
		m.readerMu.Lock()
		protected := m.generationReaders[in.SnapshotID] > 0 || m.leaseSnapshotProtected(in.SnapshotID)
		m.readerMu.Unlock()
		if protected {
			return out, ErrConflict
		}
		quality = "deleting"
		if _, err = tx.ExecContext(ctx, `DELETE FROM active_generations WHERE profile_id=? AND snapshot_id=?`, in.ProfileID, in.SnapshotID); err != nil {
			return out, err
		}
	}
	// An explicit recheck does not make a previously corrupt generation readable while verification runs.
	if _, err = tx.ExecContext(ctx, `INSERT INTO integrity_generations VALUES(?,?,?,?,?,?,?,NULL) ON CONFLICT(snapshot_id) DO UPDATE SET quality=CASE WHEN excluded.quality='deleting' THEN 'deleting' WHEN integrity_generations.quality IN ('corrupt','unsupported') THEN integrity_generations.quality ELSE 'unverified' END,stamp=excluded.stamp,owned=excluded.owned,binding=excluded.binding`, in.SnapshotID, in.ProfileID, job, quality, stamp, owned, string(bindingRaw)); err != nil {
		return out, err
	}
	out = IntegrityReceipt{randomID(), in.Operation, in.ProfileID, in.SnapshotID, "queued"}
	if _, err = tx.ExecContext(ctx, `INSERT INTO integrity_operations(id,profile_id,snapshot_id,operation,state,stamp,binding) VALUES(?,?,?,?,'queued',?,?)`, out.OperationID, in.ProfileID, in.SnapshotID, in.Operation, stamp, string(bindingRaw)); err != nil {
		return out, err
	}
	encoded, _ := json.Marshal(out)
	if err = RecordReplay(tx, replay, encoded); err != nil {
		return out, err
	}
	err = tx.Commit()
	if err == nil {
		if quality == "unverified" {
			if old, ok := m.integrityStates.Load(in.SnapshotID); ok {
				prior := old.(integrityGuard).quality
				if prior == "corrupt" || prior == "unsupported" {
					quality = prior
				}
			}
		}
		m.integrityStates.Store(in.SnapshotID, integrityGuard{quality, stamp})
	} else if in.Operation == "delete" {
		m.integrityStates.Store(in.SnapshotID, integrityGuard{"deleting", stamp})
	}
	return out, err
}

// IntegrityStatus bounds history by generation count and never publishes diagnostic SQL or source names.
func (m *Manager) IntegrityStatus(ctx context.Context, profile string) (IntegrityStatus, error) {
	out := IntegrityStatus{ProfileID: profile, SupportedSchema: index.GenerationSchema, Generations: []IntegrityEntry{}}
	if !identifier.MatchString(profile) {
		return out, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	if m.closed {
		return out, ErrUnavailable
	}
	rows, err := m.db.QueryContext(ctx, `SELECT json_extract(j.outcome_json,'$.job.resultSnapshotId'),coalesce(g.quality,'unverified'),coalesce((SELECT state FROM integrity_operations o WHERE o.snapshot_id=json_extract(j.outcome_json,'$.job.resultSnapshotId') ORDER BY o.rowid DESC LIMIT 1),'idle'),g.checked_at,coalesce(g.owned,0),coalesce(g.stamp,'') FROM jobs j LEFT JOIN integrity_generations g ON g.snapshot_id=json_extract(j.outcome_json,'$.job.resultSnapshotId') WHERE j.state='completed' AND json_extract(j.outcome_json,'$.job.profileId')=? AND json_extract(j.outcome_json,'$.job.resultSnapshotId') IS NOT NULL ORDER BY j.rowid DESC LIMIT 33`, profile)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var e IntegrityEntry
		var at sql.NullString
		var owned int
		var stamp string
		if err = rows.Scan(&e.SnapshotID, &e.Quality, &e.OperationState, &at, &owned, &stamp); err != nil {
			return out, err
		}
		if len(out.Generations) == 32 {
			out.Truncated = true
			break
		}
		if at.Valid {
			e.CheckedAt = &at.String
		}
		if e.Quality == "healthy" {
			root, file, x := m.openRetentionFile(e.SnapshotID)
			if x != nil {
				e.Quality = "unverified"
			} else {
				info, x := file.Stat()
				if x != nil || retainedStamp(info) != stamp {
					e.Quality = "unverified"
				}
				file.Close()
				root.Close()
			}
		}
		e.Deletable = owned == 1 && e.Quality != "deleted" && e.Quality != "deleting"
		out.Generations = append(out.Generations, e)
	}
	return out, rows.Err()
}
