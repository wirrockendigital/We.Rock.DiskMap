package jobs

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"sync"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// A held persistence sink cannot stall scanner callbacks or allocate a writer per sample.
func TestProgressCoalescesSlowSinkAndJoinsLatest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	records := []progressRecord{}
	observe, finish := coalescedProgress(progressRecord{}, time.Hour, func(r progressRecord) {
		mu.Lock()
		first := len(records) == 0
		records = append(records, r)
		mu.Unlock()
		if first {
			close(entered)
			<-release
		}
	})
	<-entered
	submitted := make(chan struct{})
	go func() {
		for i := uint64(1); i <= 10000; i++ {
			observe(scanner.Progress{Entries: i, Files: i - 1, Directories: 1, KnownLogicalBytes: "9007199254740993", Warnings: 7})
		}
		close(submitted)
	}()
	select {
	case <-submitted:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("scanner blocked on sink")
	}
	joined := make(chan struct{})
	go func() { finish(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("finish returned before sink")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("final join blocked")
	}
	finish()
	observe(scanner.Progress{Entries: 10001})
	mu.Lock()
	defer mu.Unlock()
	if len(records) != 2 {
		t.Fatalf("coalescing writes=%d", len(records))
	}
	last := records[1]
	if !last.Observed || last.Sample.Entries != 10000 || last.Sample.Files != 9999 || last.Sample.KnownLogicalBytes != "9007199254740993" || last.Sample.Warnings != 7 {
		t.Fatalf("lost final sample: %+v", last)
	}
}

// Pending samples wait a complete cadence after a slow write, rather than catching up in a burst.
func TestProgressCadenceStartsAfterWrite(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	writes := make(chan time.Time, 4)
	calls := 0
	observe, finish := coalescedProgress(progressRecord{}, 80*time.Millisecond, func(progressRecord) {
		calls++
		writes <- time.Now()
		if calls == 1 {
			close(entered)
			<-release
		}
	})
	<-entered
	observe(scanner.Progress{Entries: 1, KnownLogicalBytes: "1"})
	time.Sleep(100 * time.Millisecond)
	completed := time.Now()
	close(release)
	<-writes
	select {
	case at := <-writes:
		if at.Sub(completed) < 70*time.Millisecond {
			t.Fatal("write cadence counted blocked sink time")
		}
	case <-time.After(time.Second):
		t.Fatal("pending sample not written")
	}
	finish()
}

// A cancelled service context cannot suppress or deadlock the final bounded persistence attempt.
func TestProgressFinalFlushAfterCancellation(t *testing.T) {
	m := statusFixture(t)
	if err := initializeProgress(context.Background(), m.db); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.ctx = ctx
	observe, finish := m.trackProgress(Outcome{Job: contracts.Job{ID: "cancelled-progress", ProfileID: "p", ProfileRevision: 1}})
	cancel()
	observe(scanner.Progress{Entries: 12, Files: 11, Directories: 1, KnownLogicalBytes: "25"})
	finish()
	var raw string
	if err := m.db.QueryRow(`SELECT detail_json FROM job_progress WHERE job_id='cancelled-progress'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var got progressRecord
	if json.Unmarshal([]byte(raw), &got) != nil || !got.Observed || got.Sample.Entries != 12 || got.Sample.KnownLogicalBytes != "25" {
		t.Fatal("final cancelled sample missing")
	}
}
