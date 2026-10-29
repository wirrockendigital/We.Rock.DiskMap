package jobs

import (
	"context"
	"errors"
	"time"
)

var ErrViewExpired = errors.New("VIEW_EXPIRED")
var ErrViewLimit = errors.New("VIEW_LIMIT")

const viewTTL = 120 * time.Second
const maxViews = 128
const maxPrincipalViews = 16
const maxViewRecords = 1024

// viewLease holds identities only; database descriptors are pinned for individual requests.
type viewLease struct {
	Owner, Profile, Snapshot, Intent string
	Selected, Fallback               string
	Expires, Forget                  time.Time
	Released                         bool
}

// ViewLease is an opaque owner-bound handle, never a source path or authentication token.
type ViewLease struct {
	ID         string `json:"id"`
	SnapshotID string `json:"snapshotId"`
	ExpiresAt  string `json:"expiresAt"`
}
type ViewSelection struct {
	NodeID   *string `json:"nodeId"`
	Fallback string  `json:"fallback"`
}
type ReaderView struct {
	View                  SnapshotView  `json:"view"`
	Lease                 *ViewLease    `json:"lease"`
	NewCompleteSnapshotID *string       `json:"newCompleteSnapshotId"`
	Selection             ViewSelection `json:"selection"`
}

// viewNow permits deterministic expiry testing without public runtime configuration.
func (m *Manager) viewNow() time.Time {
	if m.viewClock != nil {
		return m.viewClock()
	}
	return time.Now()
}

// leaseSnapshotProtected must be called with readerMu held by cleanup and lease reservation alike.
func (m *Manager) leaseSnapshotProtected(snapshot string) bool {
	now := m.viewNow()
	for _, l := range m.viewLeases {
		if l.Snapshot == snapshot && !l.Released && now.Before(l.Expires) {
			return true
		}
	}
	return false
}

// OpenView makes initial acquisition idempotent by a caller-generated view ID; reads never change snapshots.
func (m *Manager) OpenView(ctx context.Context, owner, profile, viewID, jobID string, renew bool) (ReaderView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openViewLocked(ctx, owner, profile, viewID, jobID, renew, "", "open|"+jobID)
}

// openViewLocked keeps profile deletion and lease acquisition in one lifecycle lock.
func (m *Manager) openViewLocked(ctx context.Context, owner, profile, viewID, jobID string, renew bool, fixed, intent string) (ReaderView, error) {
	out := ReaderView{Selection: ViewSelection{Fallback: "none"}}
	if owner == "" || len(owner) > 256 || !identifier.MatchString(profile) || !identifier.MatchString(viewID) {
		return out, ErrInvalid
	}
	if m.closed {
		return out, ErrUnavailable
	}
	m.readerMu.Lock()
	now := m.viewNow()
	lease, exists := m.viewLeases[viewID]
	if exists {
		if lease.Owner != owner || lease.Profile != profile {
			m.readerMu.Unlock()
			return out, ErrNotFound
		}
		if lease.Released || !now.Before(lease.Expires) {
			m.readerMu.Unlock()
			return out, ErrViewExpired
		}
		if !renew && lease.Intent != intent {
			m.readerMu.Unlock()
			return out, ErrIdempotencyConflict
		}
		lease.Expires = now.Add(viewTTL)
	} else if renew {
		m.readerMu.Unlock()
		return out, ErrViewExpired
	}
	if exists {
		// Reserve cleanup exclusion before releasing the lease lock, including the gap before opening SQLite.
		if m.generationReaders == nil {
			m.generationReaders = map[string]int{}
		}
		m.generationReaders[lease.Snapshot]++
	}
	m.readerMu.Unlock()
	if exists {
		defer func() { m.readerMu.Lock(); m.generationReaders[lease.Snapshot]--; m.readerMu.Unlock() }()
		var err error
		out.View, err = m.snapshotLocked(ctx, profile, "", lease.Snapshot)
		if err != nil {
			return out, ErrViewExpired
		}
	} else {
		var err error
		if fixed != "" {
			var count int
			err = m.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE state='completed' AND json_extract(outcome_json,'$.job.profileId')=? AND json_extract(outcome_json,'$.job.resultSnapshotId')=?`, profile, fixed).Scan(&count)
			if err != nil {
				return out, err
			}
			if count != 1 {
				return out, ErrNotFound
			}
		}
		out.View, err = m.snapshotLocked(ctx, profile, jobID, fixed)
		if err != nil {
			return out, err
		}
		if out.View.SnapshotID == nil {
			return out, nil
		}
		m.readerMu.Lock()
		// Expired records retain a bounded tombstone so delayed acquire/release retries cannot repin another generation.
		live, mine := 0, 0
		for id, l := range m.viewLeases {
			if !now.Before(l.Forget) && (l.Released || !now.Before(l.Expires)) {
				delete(m.viewLeases, id)
				continue
			}
			if !l.Released && now.Before(l.Expires) {
				live++
				if l.Owner == owner {
					mine++
				}
			}
		}
		if live >= maxViews || mine >= maxPrincipalViews || len(m.viewLeases) >= maxViewRecords {
			m.readerMu.Unlock()
			return out, ErrViewLimit
		}
		if m.viewLeases == nil {
			m.viewLeases = map[string]*viewLease{}
		}
		lease = &viewLease{Intent: intent, Owner: owner, Profile: profile, Snapshot: *out.View.SnapshotID, Expires: now.Add(viewTTL), Forget: now.Add(24 * time.Hour)}
		m.viewLeases[viewID] = lease
		m.readerMu.Unlock()
	}
	if lease.Selected != "" {
		id := lease.Selected
		out.Selection = ViewSelection{NodeID: &id, Fallback: lease.Fallback}
		if err := m.includeSelected(ctx, &out, lease.Snapshot, id); err != nil {
			return out, err
		}
	}
	out.Lease = &ViewLease{ID: viewID, SnapshotID: lease.Snapshot, ExpiresAt: lease.Expires.UTC().Format(time.RFC3339Nano)}
	active, err := m.activeGeneration(ctx, profile)
	if err == nil && active != "" && active != lease.Snapshot {
		out.NewCompleteSnapshotID = &active
	}
	return out, nil
}

// ReleaseView is harmless when repeated, but cannot release another principal's view.
func (m *Manager) ReleaseView(owner, profile, id string) error {
	if owner == "" || !identifier.MatchString(profile) || !identifier.MatchString(id) {
		return ErrInvalid
	}
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	l := m.viewLeases[id]
	if l == nil {
		return nil
	}
	if l.Owner != owner || l.Profile != profile {
		return ErrNotFound
	}
	l.Released = true
	return nil
}

// OpenRetainedView opens only an explicitly requested, complete, profile-bound historical result.
func (m *Manager) OpenRetainedView(ctx context.Context, owner, profile, viewID, snapshot string) (ReaderView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !identifier.MatchString(snapshot) {
		return ReaderView{}, ErrInvalid
	}
	if err := m.qualifyRetainedTarget(ctx, profile, snapshot); err != nil {
		return ReaderView{}, err
	}
	out, err := m.openViewLocked(ctx, owner, profile, viewID, "", false, snapshot, "open-retained|"+snapshot)
	if err == nil && (out.View.State == nil || *out.View.State != "complete") {
		_ = m.ReleaseView(owner, profile, viewID)
		return ReaderView{}, ErrInvalid
	}
	return out, err
}

// qualifyRetainedTarget applies the same current integrity evidence to every historical acquisition route.
func (m *Manager) qualifyRetainedTarget(ctx context.Context, profile, snapshot string) error {
	// Initial historical acquisition requires the same current integrity evidence as its catalogue entry.
	var stamp string
	if m.db.QueryRowContext(ctx, `SELECT stamp FROM retention_qualification WHERE snapshot_id=? AND profile_id=?`, snapshot, profile).Scan(&stamp) != nil || stamp == "" || stamp == "removed" {
		return ErrUnavailable
	}
	root, file, e := m.openRetentionFile(snapshot)
	if e != nil {
		return ErrUnavailable
	}
	info, e := file.Stat()
	file.Close()
	root.Close()
	if e != nil || retainedStamp(info) != stamp {
		return ErrUnavailable
	}
	return nil
}
