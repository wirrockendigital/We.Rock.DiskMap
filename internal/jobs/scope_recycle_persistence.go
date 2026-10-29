package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// recycleCacheRecord persists only the already derived internal category root observations.
type recycleCacheRecord struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	Bytes  string `json:"bytes"`
}

// persistedRecycleObservations avoids repeating a million-directory lookup after restart.
func (m *Manager) persistedRecycleObservations(ctx context.Context, db *sql.DB, snapshot string) ([]recycleObservation, bool) {
	raw := m.readQueryCache(m.querySourceKey(ctx, db, snapshot, "recycle"))
	if len(raw) == 0 || len(raw) > 8<<20 {
		return nil, false
	}
	var records []recycleCacheRecord
	if json.Unmarshal(raw, &records) != nil || len(records) > 100000 {
		return nil, false
	}
	out := make([]recycleObservation, 0, len(records))
	for _, r := range records {
		n, ok := new(big.Int).SetString(r.Bytes, 10)
		if !ok || n.Sign() < 0 || r.Target == "" || r.Path == "" || len(r.Path) > 4096 {
			return nil, false
		}
		out = append(out, recycleObservation{r.Target, r.Path, r.Bytes})
	}
	return out, true
}

// persistRecycleObservations is best effort and cannot change the authoritative scan result.
func (m *Manager) persistRecycleObservations(ctx context.Context, db *sql.DB, snapshot string, roots []recycleObservation) {
	records := make([]recycleCacheRecord, 0, len(roots))
	for _, r := range roots {
		records = append(records, recycleCacheRecord{r.target, r.path, r.bytes})
	}
	raw, err := json.Marshal(records)
	if err == nil && len(raw) <= 8<<20 {
		m.writeQueryCache(m.querySourceKey(ctx, db, snapshot, "recycle"), raw)
	}
}

// recycleFromCategories resolves only topmost recycle IDs already found by exact category preparation.
func recycleFromCategories(ctx context.Context, db *sql.DB, c *query.MapCategories) ([]recycleObservation, error) {
	reader, err := index.NewNodeReader(ctx, db)
	if err != nil {
		return nil, err
	}
	out := []recycleObservation{}
	for id, d := range c.Dirs {
		if d.Flags&2 == 0 {
			continue
		}
		if p := c.Dirs[d.Parent]; p != nil && p.Flags&2 != 0 {
			continue
		}
		key := strconv.FormatInt(id, 10)
		n, err := reader.Read(key)
		if err != nil {
			return nil, err
		}
		a, err := index.ReadDirectoryAggregate(ctx, db, key)
		if err != nil {
			return nil, err
		}
		out = append(out, recycleObservation{n.TargetID, string(n.Path), string(a.Subtree.Logical.KnownBytes)})
	}
	return out, nil
}

// preparePublishedQueries completes expensive immutable projections before announcing a new result.
// A failed optional cache write does not fail publication; authoritative read errors remain explicit.
func (m *Manager) preparePublishedQueries(ctx context.Context, snapshot string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	db, release, err := m.generationDB(snapshot)
	if err != nil {
		return err
	}
	defer release()
	c, err := m.preparedMapCategories(ctx, db, snapshot)
	if err != nil {
		return err
	}
	roots, err := recycleFromCategories(ctx, db, c)
	if err != nil {
		return err
	}
	m.persistRecycleObservations(ctx, db, snapshot, roots)
	return nil
}
