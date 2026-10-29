package jobs

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"math/big"
	"strconv"
	"strings"
	"time"
)

type recycleObservation struct{ target, path, bytes string }

// Read category roots once per metadata batch, then reuse exact stored directory aggregates.
func readRecycleObservations(ctx context.Context, db *sql.DB) (result []recycleObservation, resultErr error) {
	finish := diagnostics.Measure(ctx, "scope_recycle", 0)
	defer func() { finish(resultErr) }()
	// Drive the lookup from directory IDs, avoiding a full multi-million-file table scan.
	rows, err := db.QueryContext(ctx, `SELECT n.id FROM directory_aggregates a CROSS JOIN nodes n ON n.id=a.node_id WHERE lower(CAST(n.name_bytes AS TEXT))='#recycle'`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	reader, err := index.NewNodeReader(ctx, db)
	if err != nil {
		return nil, err
	}
	out := []recycleObservation{}
	for _, id := range ids {
		n, e := reader.Read(id)
		if e != nil {
			return nil, e
		}
		p := string(n.Path)
		// Nested recycle folders already belong to their outer recycle subtree.
		parts := strings.Split(p, "/")
		nested := false
		for _, part := range parts[:len(parts)-1] {
			if strings.EqualFold(part, "#recycle") {
				nested = true
			}
		}
		if nested {
			continue
		}
		a, e := index.ReadDirectoryAggregate(ctx, db, id)
		if e != nil {
			return nil, e
		}
		out = append(out, recycleObservation{n.TargetID, p, string(a.Subtree.Logical.KnownBytes)})
	}
	return out, nil
}

// Recycling is independent of system-file visibility and never counted twice.
func recycleBytes(roots []recycleObservation, target, path, scopePath, total string) string {
	for _, part := range strings.Split(scopePath, "/") {
		if strings.EqualFold(part, "#recycle") {
			return total
		}
	}
	sum := new(big.Int)
	for _, r := range roots {
		if r.target != target {
			continue
		}
		if path == r.path || strings.HasPrefix(path, r.path+"/") {
			return total
		}
		if path == "." || strings.HasPrefix(r.path, path+"/") {
			n, _ := new(big.Int).SetString(r.bytes, 10)
			sum.Add(sum, n)
		}
	}
	return sum.String()
}

// A pending slot coalesces concurrent misses; ready slots use bounded LRU eviction.
type recycleObservationSlot struct {
	ready chan struct{}
	value []recycleObservation
	err   error
	used  time.Time
}

// SQL runs outside the cache mutex; waiting callers retain their own cancellation deadline.
func (m *Manager) cachedRecycleObservations(ctx context.Context, db *sql.DB, snapshot string) ([]recycleObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.recycleMu.Lock()
	if slot := m.recycleCache[snapshot]; slot != nil {
		slot.used = time.Now()
		m.recycleMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-slot.ready:
			return slot.value, slot.err
		}
	}
	if len(m.recycleCache) >= 8 {
		victim := ""
		var oldest time.Time
		for key, slot := range m.recycleCache {
			select {
			case <-slot.ready:
				if victim == "" || slot.used.Before(oldest) {
					victim, oldest = key, slot.used
				}
			default:
			}
		}
		if victim == "" {
			m.recycleMu.Unlock()
			return nil, ErrUnavailable
		}
		delete(m.recycleCache, victim)
	}
	if m.recycleCache == nil {
		m.recycleCache = map[string]*recycleObservationSlot{}
	}
	slot := &recycleObservationSlot{ready: make(chan struct{}), used: time.Now()}
	m.recycleCache[snapshot] = slot
	m.recycleMu.Unlock()
	value, cached := m.persistedRecycleObservations(ctx, db, snapshot)
	var err error
	if !cached {
		value, err = readRecycleObservations(ctx, db)
		if err == nil {
			m.persistRecycleObservations(ctx, db, snapshot, value)
		}
	}
	m.recycleMu.Lock()
	slot.value, slot.err = value, err
	if err != nil {
		delete(m.recycleCache, snapshot)
	}
	close(slot.ready)
	m.recycleMu.Unlock()
	return value, err
}
