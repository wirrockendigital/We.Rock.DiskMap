package jobs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// ErrFilterUnavailable concerns disposable derived data, never source integrity.
var ErrFilterUnavailable = errors.New("FILTER_UNAVAILABLE")

const filterCacheBudget uint64 = 16 << 30

// filterCacheManager serializes one builder and bounds retained ready projections.
type filterCacheManager struct {
	mu               sync.Mutex
	cond             *sync.Cond
	dir              *os.File
	entries          map[string]*filterCacheEntry
	building         *filterCacheEntry
	wake             chan struct{}
	disabled, closed bool
	tick             uint64
	invalidated      map[string]bool
}
type filterCacheEntry struct {
	key, state  string
	header      index.GenerationHeader
	filter      query.Filter
	metric      string
	file        *os.File
	marker      *os.File
	db          *sql.DB
	source      *sql.DB
	sourceClose func()
	refs        int
	used        uint64
	stamp       string
}

// initializeFilterCache isolates unsafe or unavailable cache storage from core functionality.
func (m *Manager) initializeFilterCache() error {
	c := &filterCacheManager{entries: make(map[string]*filterCacheEntry), wake: make(chan struct{}, 1)}
	c.cond = sync.NewCond(&c.mu)
	m.filterCache = c
	dir, err := openFilterCacheDirectory(m.state)
	if err == nil {
		err = discardPriorFilterCaches(m.ctx, dir)
	}
	if err != nil {
		if dir != nil {
			dir.Close()
		}
		c.disabled = true
		return nil
	}
	c.dir = dir
	return nil
}

// ensureFilterCache requires an already authorized, pinned generation and never returns unfinished totals.
func (m *Manager) ensureFilterCache(ctx context.Context, h index.GenerationHeader, f query.Filter, metric string) (*sql.DB, func(), string, error) {
	canonical, err := f.Canonical()
	if err != nil {
		return nil, nil, "", err
	}
	key, err := query.FilterProjectionKey(h, metric, canonical)
	if err != nil {
		return nil, nil, "", err
	}
	c := m.filterCache
	if c == nil {
		return nil, nil, "", ErrFilterUnavailable
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return nil, nil, "", ctx.Err()
	}
	if c.disabled || c.closed || c.invalidated[h.SnapshotID] || m.ctx.Err() != nil {
		return nil, nil, "", ErrFilterUnavailable
	}
	c.tick++
	if e := c.entries[key]; e != nil {
		e.used = c.tick
		if e.state == "pending" {
			return nil, nil, "pending", nil
		}
		if e.state != "ready" || !filterCacheIdentity(c.dir, e.file, e.key, e.stamp) {
			return nil, nil, "", ErrFilterUnavailable
		}
		e.refs++
		var once sync.Once
		return e.db, func() { once.Do(func() { c.mu.Lock(); e.refs--; c.cond.Broadcast(); c.mu.Unlock() }) }, "ready", nil
	}
	if c.building != nil {
		return nil, nil, "", ErrFilterUnavailable
	}
	// Failed entries remain explicit until a distinct request evicts them; they never retry indefinitely.
	for len(c.entries) >= 2 {
		var victim *filterCacheEntry
		for _, e := range c.entries {
			if e.refs == 0 && (victim == nil || e.used < victim.used) {
				victim = e
			}
		}
		if victim == nil {
			return nil, nil, "", ErrFilterUnavailable
		}
		if err = c.evict(victim); err != nil {
			c.disabled = true
			return nil, nil, "", ErrFilterUnavailable
		}
	}
	free, err := m.availableStateBytes()
	if err != nil || free <= IndexReserveBytes {
		return nil, nil, "", ErrFilterUnavailable
	}
	// Acquire the build reservation before the caller can release its request reservation.
	source, release, err := m.generationDB(h.SnapshotID)
	if err != nil {
		return nil, nil, "", ErrFilterUnavailable
	}
	actual, err := index.ReadGenerationHeader(ctx, source, h.SnapshotID)
	actualKey, keyErr := query.FilterProjectionKey(actual, metric, canonical)
	if err != nil || keyErr != nil || actualKey != key {
		release()
		return nil, nil, "", ErrFilterUnavailable
	}
	e := &filterCacheEntry{key: key, state: "pending", header: h, filter: canonical, metric: metric, source: source, sourceClose: release, used: c.tick}
	c.entries[key] = e
	c.building = e
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil, nil, "pending", nil
}

// startFilterCacheWorker is the sole writer; Close joins it before closing the control database.
func (m *Manager) startFilterCacheWorker() {
	c := m.filterCache
	if c == nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer c.shutdown()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-c.wake:
				c.mu.Lock()
				e := c.building
				c.mu.Unlock()
				if e == nil {
					continue
				}
				err := m.buildFilterCache(e)
				c.mu.Lock()
				if e.sourceClose != nil {
					e.sourceClose()
					e.sourceClose = nil
					e.source = nil
				}
				if err != nil || m.ctx.Err() != nil {
					e.state = "unavailable"
					if e.db != nil {
						e.db.Close()
						e.db = nil
					}
				} else {
					e.state = "ready"
				}
				c.building = nil
				c.mu.Unlock()
			}
		}
	}()
}

// shutdown drains bounded readers before closing their shared read-only handles.
func (c *filterCacheManager) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for {
		refs := 0
		for _, e := range c.entries {
			refs += e.refs
		}
		if refs == 0 {
			break
		}
		c.cond.Wait()
	}
	for _, e := range c.entries {
		if e.sourceClose != nil {
			e.sourceClose()
			e.sourceClose = nil
		}
		if e.db != nil {
			e.db.Close()
		}
		if e.file != nil {
			e.file.Close()
		}
		if e.marker != nil {
			e.marker.Close()
		}
	}
	if c.dir != nil {
		c.dir.Close()
	}
}

// buildFilterCache performs bounded transactional steps without holding lifecycle or cache locks.
func (m *Manager) buildFilterCache(e *filterCacheEntry) error {
	c := m.filterCache
	file, err := createFilterCacheFile(c.dir, e.key)
	if err != nil {
		return err
	}
	// Only the worker writes fields while pending; readers inspect them only after its mutex publication.
	e.file = file
	e.marker, err = createFilterCacheMarker(c.dir, e)
	if err != nil {
		return err
	}
	db, err := filterCacheDB(file, false)
	if err != nil {
		return err
	}
	e.db = db
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	_, err = db.ExecContext(ctx, `PRAGMA journal_mode=MEMORY; PRAGMA synchronous=OFF; PRAGMA mmap_size=0; PRAGMA cache_size=-2048`)
	if err == nil {
		err = m.limitFilterCache(e)
	}
	if err == nil {
		_, err = db.ExecContext(ctx, `CREATE TABLE filter_cache_owner(version INTEGER NOT NULL,key TEXT NOT NULL,snapshot TEXT NOT NULL,object TEXT NOT NULL); INSERT INTO filter_cache_owner VALUES(2,?,?,?)`, e.key, e.header.SnapshotID, filterCacheObject(file))
	}
	if err == nil {
		err = query.InitializeFilterProjection(ctx, e.source, db, e.header, e.metric, e.filter)
	}
	cancel()
	if err != nil {
		return err
	}
	for {
		if err = m.ctx.Err(); err != nil {
			return err
		}
		if err = m.limitFilterCache(e); err != nil {
			return err
		}
		ctx, cancel = context.WithTimeout(m.ctx, 3*time.Second)
		progress, stepErr := query.BuildFilterProjectionStep(ctx, e.source, db, e.header, e.metric, e.filter, 256)
		cancel()
		if stepErr != nil {
			return stepErr
		}
		if progress.Complete {
			break
		}
	}
	// Durability is required only at the ready boundary; a crash during construction causes reconstruction.
	if err = db.Close(); err != nil {
		return err
	}
	e.db = nil
	if err = file.Sync(); err != nil {
		return err
	}
	if err = c.dir.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	e.stamp = retainedStamp(info)
	if !filterCacheIdentity(c.dir, file, e.key, e.stamp) {
		return ErrFilterUnavailable
	}
	db, err = filterCacheDB(file, true)
	if err != nil {
		return err
	}
	e.db = db
	ctx, cancel = context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	if err = query.ValidateFilterProjection(ctx, db, e.header, e.metric, e.filter); err != nil {
		return ErrFilterUnavailable
	}
	if !filterCacheIdentity(c.dir, file, e.key, e.stamp) {
		return ErrFilterUnavailable
	}
	return m.ctx.Err()
}

// limitFilterCache caps database growth and preserves an operational free-space reserve before each step.
func (m *Manager) limitFilterCache(e *filterCacheEntry) error {
	c := m.filterCache
	c.mu.Lock()
	defer c.mu.Unlock()
	var total, current uint64
	for _, entry := range c.entries {
		if entry.file != nil {
			info, err := entry.file.Stat()
			if err != nil || info.Size() < 0 {
				return ErrFilterUnavailable
			}
			size := uint64(info.Size())
			if size > filterCacheBudget-total {
				return ErrFilterUnavailable
			}
			total += size
			if entry == e {
				current = size
			}
		}
	}
	free, err := m.availableStateBytes()
	if err != nil || free <= IndexReserveBytes {
		return ErrFilterUnavailable
	}
	allowance := filterCacheBudget - total
	if free-IndexReserveBytes < allowance {
		allowance = free - IndexReserveBytes
	}
	var pageSize uint64
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
	defer cancel()
	if err = e.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil || pageSize == 0 {
		return ErrFilterUnavailable
	}
	pages := (current + allowance) / pageSize
	if pages == 0 {
		return ErrFilterUnavailable
	}
	return setFilterCachePages(ctx, e.db, pages)
}
