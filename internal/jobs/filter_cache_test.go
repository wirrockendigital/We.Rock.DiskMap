package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// cacheFixture obtains an actual sealed scanner result instead of manufacturing source metadata.
func cacheFixture(t *testing.T) (*Manager, index.GenerationHeader, string) {
	t.Helper()
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	outcome := terminal(t, m, result.Job.ID)
	if outcome.Job.State != "completed" {
		t.Fatal(outcome)
	}
	id := *outcome.Job.ResultSnapshotID
	db, close, err := m.generationDB(id)
	if err != nil {
		t.Fatal(err)
	}
	h, err := index.ReadGenerationHeader(context.Background(), db, id)
	close()
	if err != nil {
		t.Fatal(err)
	}
	return m, h, filepath.Join(m.state, "generations", id, "index.db")
}

// waitCache only accepts the explicit ready boundary, with a small bounded local fixture deadline.
func waitCache(t *testing.T, m *Manager, h index.GenerationHeader, f query.Filter) (*sql.DB, func()) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		db, release, state, err := m.ensureFilterCache(context.Background(), h, f, "logical")
		if err != nil {
			t.Fatal(err)
		}
		if state == "ready" {
			return db, release
		}
		if state != "pending" || db != nil || release != nil {
			t.Fatal("unfinished projection escaped")
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cache did not become ready")
	return nil, nil
}

// TestFilterCacheLifecycle proves exact source binding, completed data, immutable source bytes and ready reuse.
func TestFilterCacheLifecycle(t *testing.T) {
	m, h, path := cacheFixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f := query.Filter{Text: "data", Field: "name"}
	db, release := waitCache(t, m, h, f)
	defer release()
	if err = query.ValidateFilterProjection(context.Background(), db, h, "logical", f); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM filter_nodes`).Scan(&count); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if _, err = db.Exec(`DELETE FROM filter_nodes`); err == nil {
		t.Fatal("ready cache is writable")
	}
	again, releaseAgain, state, err := m.ensureFilterCache(context.Background(), h, f, "logical")
	if err != nil || state != "ready" || again != db {
		t.Fatal(state, err)
	}
	releaseAgain()
	releaseAgain()
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source changed", err)
	}
	wrong := h
	wrong.AggregateVersion++
	if _, _, _, err = m.ensureFilterCache(context.Background(), wrong, f, "logical"); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal("wrong header accepted", err)
	}
}

// TestFilterCacheActiveReadersAndReplacement protects retained handles and rejects exchanged directory entries.
func TestFilterCacheActiveReadersAndReplacement(t *testing.T) {
	m, h, _ := cacheFixture(t)
	_, releaseOne := waitCache(t, m, h, query.Filter{Text: "data", Field: "name"})
	_, releaseTwo := waitCache(t, m, h, query.Filter{Text: "missing", Field: "name"})
	if _, _, _, err := m.ensureFilterCache(context.Background(), h, query.Filter{Text: "third", Field: "name"}, "logical"); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal("evicted active reader", err)
	}
	releaseOne()
	releaseTwo()
	key, _ := query.FilterProjectionKey(h, "logical", query.Filter{Text: "data", Field: "name"})
	path := filepath.Join(m.state, "filter-cache", key+".db")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.ensureFilterCache(context.Background(), h, query.Filter{Text: "data", Field: "name"}, "logical"); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal("replacement accepted", err)
	}
}

// TestFilterCacheLowSpaceAndCancel bounds failure and releases every build reservation on shutdown.
func TestFilterCacheLowSpaceAndCancel(t *testing.T) {
	m, h, _ := cacheFixture(t)
	// A standalone manager avoids changing a probe while unrelated maintenance workers are active.
	low := &Manager{ctx: context.Background(), filterCache: &filterCacheManager{entries: map[string]*filterCacheEntry{}}, spaceProbe: func() (uint64, error) { return IndexReserveBytes, nil }}
	if _, _, _, err := low.ensureFilterCache(context.Background(), h, query.Filter{}, "logical"); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal(err)
	}
	_, _, state, err := m.ensureFilterCache(context.Background(), h, query.Filter{}, "logical")
	if err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m.readerMu.Lock()
	defer m.readerMu.Unlock()
	if m.generationReaders[h.SnapshotID] != 0 {
		t.Fatal("build pin leaked", m.generationReaders)
	}
}

// TestFilterCacheShutdownWaitsForReaders proves the shared handle cannot close under an active response.
func TestFilterCacheShutdownWaitsForReaders(t *testing.T) {
	c := &filterCacheManager{entries: map[string]*filterCacheEntry{"test": {refs: 1}}}
	c.cond = sync.NewCond(&c.mu)
	var stopped atomic.Bool
	done := make(chan struct{})
	go func() { c.shutdown(); stopped.Store(true); close(done) }()
	time.Sleep(10 * time.Millisecond)
	if stopped.Load() {
		t.Fatal("closed under reader")
	}
	c.mu.Lock()
	c.entries["test"].refs = 0
	c.cond.Broadcast()
	c.mu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader drain did not finish")
	}
}

// TestFilterCacheWorkerFailureIsBounded preserves a foreign collision and never repeats a failed build automatically.
func TestFilterCacheWorkerFailureIsBounded(t *testing.T) {
	m, h, _ := cacheFixture(t)
	f := query.Filter{Text: "collision", Field: "name"}
	key, _ := query.FilterProjectionKey(h, "logical", f)
	path := filepath.Join(m.state, "filter-cache", key+".db")
	if err := os.WriteFile(path, []byte("preserve foreign data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, state, err := m.ensureFilterCache(context.Background(), h, f, "logical")
	if err != nil || state != "pending" {
		t.Fatal(state, err)
	}
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		_, _, state, err = m.ensureFilterCache(context.Background(), h, f, "logical")
		if errors.Is(err, ErrFilterUnavailable) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal("failed build remains pending", state, err)
	}
	for range 3 {
		if _, _, _, err = m.ensureFilterCache(context.Background(), h, f, "logical"); !errors.Is(err, ErrFilterUnavailable) {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "preserve foreign data" {
		t.Fatal("foreign cache modified", err)
	}
}

// TestFilterCacheDiskBudget uses a sparse local file to test the hard byte ceiling without allocating large RAM or disk.
func TestFilterCacheDiskBudget(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "bounded")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err = file.Truncate(int64(filterCacheBudget) + 1); err != nil {
		t.Fatal(err)
	}
	e := &filterCacheEntry{file: file}
	m := &Manager{filterCache: &filterCacheManager{entries: map[string]*filterCacheEntry{"oversize": e}}}
	if err = m.limitFilterCache(e); !errors.Is(err, ErrFilterUnavailable) {
		t.Fatal("oversized cache accepted", err)
	}
}
