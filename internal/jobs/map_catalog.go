package jobs

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"time"
)

type mapCatalogSlot struct {
	ready chan struct{}
	data  *query.MapCategories
	err   error
}

// At most two immutable directory catalogs are retained. No file names or source handles are cached.
func (m *Manager) mapCatalog(caller context.Context, snapshot string) (*query.MapCategories, bool, error) {
	m.mapCatalogMu.Lock()
	defer m.mapCatalogMu.Unlock()
	if m.mapCatalogs == nil {
		m.mapCatalogs = map[string]*mapCatalogSlot{}
	}
	if slot := m.mapCatalogs[snapshot]; slot != nil {
		select {
		case <-slot.ready:
			return slot.data, false, slot.err
		default:
			return nil, true, nil
		}
	}
	if len(m.mapCatalogs) >= 2 {
		removed := false
		for id, slot := range m.mapCatalogs {
			select {
			case <-slot.ready:
				delete(m.mapCatalogs, id)
				removed = true
			default:
			}
			if removed {
				break
			}
		}
		if !removed {
			return nil, true, nil
		}
	}
	db, closeDB, err := m.generationDB(snapshot)
	if err != nil {
		return nil, false, err
	}
	slot := &mapCatalogSlot{ready: make(chan struct{})}
	m.mapCatalogs[snapshot] = slot
	finish := diagnostics.Measure(caller, "map_catalog", 0)
	go func() {
		defer closeDB()
		ctx, cancel := context.WithTimeout(m.ctx, 2*time.Minute)
		defer cancel()
		slot.data, slot.err = m.preparedMapCategories(ctx, db, snapshot)
		finish(slot.err)
		close(slot.ready)
	}()
	return nil, true, nil
}

// preparedMapCategories reuses exact durable numeric results across restarts and RAM eviction.
func (m *Manager) preparedMapCategories(ctx context.Context, db *sql.DB, snapshot string) (*query.MapCategories, error) {
	key := m.querySourceKey(ctx, db, snapshot, "map")
	if raw := m.readQueryCache(key); raw != nil {
		if data, err := query.DecodeMapCategories(raw); err == nil {
			return data, nil
		}
	}
	data, err := query.BuildMapCategories(ctx, db)
	if err == nil {
		m.writeQueryCache(key, query.EncodeMapCategories(data))
		// Share the category walk with size observations instead of scanning directory names again.
		if roots, e := recycleFromCategories(ctx, db, data); e == nil {
			m.persistRecycleObservations(ctx, db, snapshot, roots)
		}
	}
	return data, err
}
