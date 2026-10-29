package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// HierarchyRequest carries an existing reader identity, never a source path or a new default selection.
type HierarchyRequest struct {
	ContractVersion int           `json:"contractVersion"`
	ProfileID       string        `json:"profileId"`
	ViewID          string        `json:"viewId"`
	Query           query.Request `json:"query"`
	Cursor          string        `json:"cursor,omitempty"`
}

// Validate rejects ambiguous identities before reserving any query resources.
func (r HierarchyRequest) Validate() error {
	if r.ContractVersion != 1 || !identifier.MatchString(r.ProfileID) || !identifier.MatchString(r.ViewID) || !identifier.MatchString(r.Query.SnapshotID) || len(r.Cursor) > 160 {
		return ErrInvalid
	}
	if _, err := r.Query.Validate(); err != nil {
		return err
	}
	return nil
}

// errHierarchySlotsFull is private admission pressure, not an exhausted query work budget.
var errHierarchySlotsFull = errors.New("hierarchy slots occupied")

// errHierarchyLifecycleBusy requests a resource-free retry without disguising mutex pressure as SQL failure.
var errHierarchyLifecycleBusy = errors.New("hierarchy lifecycle occupied")

// reserveHierarchy pins an already authorized view while releasing the lifecycle lock before SQL work.
func (m *Manager) reserveHierarchy(ctx context.Context, owner string, in HierarchyRequest) (func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	var release func()
	var err error
	var waiting chan struct{}
	defer func() {
		if waiting != nil {
			<-waiting
		}
	}()
	for {
		release, err = retryControlRead(ctx, func() (func(), error) { return m.reserveHierarchyAttempt(ctx, owner, in) })
		if !errors.Is(err, errHierarchySlotsFull) && !errors.Is(err, errHierarchyLifecycleBusy) {
			break
		}
		if waiting == nil {
			if err = m.lockQuery(ctx); err != nil {
				break
			}
			if m.hierarchyWaiters == nil {
				m.hierarchyWaiters = make(chan struct{}, 16)
			}
			select {
			case m.hierarchyWaiters <- struct{}{}:
				waiting = m.hierarchyWaiters
			default:
				err = query.ErrBudget
			}
			m.mu.Unlock()
			if waiting == nil {
				break
			}
		}
		// The failed attempt released its connection, locks and slot before this bounded wait.
		// Re-admission revalidates profile, lease and snapshot rather than retaining stale authority.
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
			err = nil
		}
		if err != nil {
			break
		}
	}

	// Cancellation is controlled unavailability; deadline identity remains a query-budget error.
	if errors.Is(err, context.Canceled) {
		return nil, ErrUnavailable
	}
	return release, err
}

// reserveHierarchyAttempt releases every admission resource before a busy retry.
func (m *Manager) reserveHierarchyAttempt(ctx context.Context, owner string, in HierarchyRequest) (func(), error) {
	if in.Validate() != nil || !validPrincipal(owner) {
		return nil, ErrInvalid
	}
	// Reject only an exactly bound expired/released view before lifecycle or database contention.
	// Release readerMu before acquiring m.mu; valid views still use the atomic reservation below.
	if m.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	m.readerMu.Lock()
	known := m.viewLeases[in.ViewID]
	denied := known != nil && known.Owner == owner && known.Profile == in.ProfileID && known.Snapshot == in.Query.SnapshotID && (known.Released || !m.viewNow().Before(known.Expires))
	m.readerMu.Unlock()
	if denied {
		return nil, ErrViewExpired
	}
	// Wait for a bounded read connection before entering the lifecycle section.
	// The existence check remains serialized with profile deletion; no transaction
	// is held across lock acquisition (which could block a writer's commit).
	conn, err := m.readDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer conn.Close()
	// Never retain a scarce observation connection while waiting for lifecycle ownership.
	// The outer admission loop waits without resources and revalidates all bindings.
	if !m.mu.TryLock() {
		return nil, errHierarchyLifecycleBusy
	}
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	if m.hierarchySlots == nil {
		m.hierarchySlots = make(chan struct{}, 2)
	}
	select {
	case m.hierarchySlots <- struct{}{}:
	default:
		return nil, errHierarchySlotsFull
	}
	releaseSlot := func() { <-m.hierarchySlots }
	var exists int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM profiles WHERE id=?`, in.ProfileID).Scan(&exists); err != nil {
		releaseSlot()
		// Preserve the typed lock error until the released attempt can be retried.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if exists != 1 {
		releaseSlot()
		return nil, ErrProfileMissing
	}
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	lease := m.viewLeases[in.ViewID]
	if lease == nil || lease.Released || !m.viewNow().Before(lease.Expires) {
		releaseSlot()
		return nil, ErrViewExpired
	}
	if lease.Owner != owner || lease.Profile != in.ProfileID {
		releaseSlot()
		return nil, ErrNotFound
	}
	if lease.Snapshot != in.Query.SnapshotID {
		releaseSlot()
		return nil, query.ErrSnapshot
	}
	if m.generationReaders == nil {
		m.generationReaders = map[string]int{}
	}
	m.generationReaders[lease.Snapshot]++
	return func() {
		m.readerMu.Lock()
		m.generationReaders[lease.Snapshot]--
		if m.generationReaders[lease.Snapshot] == 0 {
			delete(m.generationReaders, lease.Snapshot)
		}
		m.readerMu.Unlock()
		releaseSlot()
	}, nil
}

// ReadHierarchy never renews or changes a lease; visible-view renewal remains a separate explicit operation.
func (m *Manager) ReadHierarchy(ctx context.Context, owner string, in HierarchyRequest, children bool) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	if !children && in.Cursor != "" {
		return nil, ErrInvalid
	}
	release, err := m.reserveHierarchy(ctx, owner, in)
	if err != nil {
		return nil, err
	}
	defer release()
	db, close, err := m.generationDB(in.Query.SnapshotID)
	if err != nil {
		return nil, ErrViewExpired
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, in.Query.SnapshotID)
	if err != nil {
		return nil, err
	}
	if header.Profile.ID != in.ProfileID {
		return nil, query.ErrSnapshot
	}
	// Build exact filter projections asynchronously while retaining the authorized snapshot identity.
	if in.Query.Filter.HasPredicates() {
		cache, releaseCache, state, cacheErr := m.ensureFilterCache(ctx, header, in.Query.Filter, in.Query.Metric)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if state == "pending" {
			return struct {
				State string        `json:"state"`
				Query query.Request `json:"query"`
			}{State: "pending", Query: in.Query}, nil
		}
		defer releaseCache()
		if children {
			return query.ChildrenFiltered(ctx, db, cache, header, in.Query, in.Cursor)
		}
		return query.HierarchyFiltered(ctx, db, cache, header, in.Query)
	}
	return executeHierarchy(ctx, db, header, in, children)
}

// executeHierarchy keeps the query package independent from lifecycle and filesystem authority.
func executeHierarchy(ctx context.Context, db *sql.DB, h index.GenerationHeader, in HierarchyRequest, children bool) (any, error) {
	if children {
		return query.Children(ctx, db, h, in.Query, in.Cursor)
	}
	return query.Hierarchy(ctx, db, h, in.Query)
}

// lockQuery waits cooperatively without extending the caller's original query deadline.
// A quiet timer avoids a spin loop while the existing lifecycle mutex remains authoritative.
func (m *Manager) lockQuery(ctx context.Context) error {
	timer := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.mu.TryLock() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}
