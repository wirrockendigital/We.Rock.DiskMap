package jobs

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A ninth generation evicts one cold slot; hot immutable observations remain reusable.
func TestRecycleObservationCacheLRUAndCancellation(t *testing.T) {
	m, in, _ := bootFixture(t)
	j, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, j.Job.ID)
	db, close, err := m.generationDB(*done.Job.ResultSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	for i := 0; i < 8; i++ {
		if _, err := m.cachedRecycleObservations(context.Background(), db, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit ordering avoids relying on the host clock resolution for LRU expectations.
	for i := 0; i < 8; i++ {
		m.recycleCache[fmt.Sprint(i)].used = time.Unix(int64(i), 0)
	}
	if _, err := m.cachedRecycleObservations(context.Background(), db, "0"); err != nil {
		t.Fatal(err)
	}
	hot := m.recycleCache["0"]
	if _, err := m.cachedRecycleObservations(context.Background(), db, "ninth"); err != nil {
		t.Fatal(err)
	}
	if len(m.recycleCache) != 8 || m.recycleCache["0"] != hot || m.recycleCache["1"] != nil {
		t.Fatal("cache was flushed or hot slot evicted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.cachedRecycleObservations(ctx, db, "0"); err == nil {
		t.Fatal("cancelled cache hit succeeded")
	}
	// A pending generation must not block independent callers beyond their own context.
	m.recycleCache["pending"] = &recycleObservationSlot{ready: make(chan struct{})}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err := m.cachedRecycleObservations(ctx, db, "pending"); err == nil {
		t.Fatal("pending wait ignored deadline")
	}
}
