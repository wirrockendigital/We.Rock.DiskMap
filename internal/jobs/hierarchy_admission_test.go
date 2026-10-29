package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// admissionFixture uses a real completed generation and an owner-bound live lease.
func admissionFixture(t *testing.T) (*Manager, HierarchyRequest) {
	t.Helper()
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, started.Job.ID)
	view, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "admission-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return m, HierarchyRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas", Metric: "logical", Depth: 1, ElementBudget: 20}}
}

// A third legitimate read must wait briefly without consuming a third active reservation.
func TestHierarchyAdmissionBurst(t *testing.T) {
	m, in := admissionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, err := m.reserveHierarchy(ctx, "admin", in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.reserveHierarchy(ctx, "admin", in)
	if err != nil {
		first()
		t.Fatal(err)
	}
	defer second()
	result := make(chan error, 1)
	go func() {
		release, e := m.reserveHierarchy(ctx, "admin", in)
		if e == nil {
			release()
		}
		result <- e
	}()
	select {
	case e := <-result:
		first()
		t.Fatalf("third read must wait, got %v", e)
	case <-time.After(40 * time.Millisecond):
	}
	first()
	select {
	case e := <-result:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// Re-admission must observe invalidation and cancellation instead of retaining prior authority.
func TestHierarchyAdmissionRevalidation(t *testing.T) {
	for _, scenario := range []string{"cancel", "deadline", "released", "expired", "profile-deleted", "snapshot-rebound", "manager-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			m, in := admissionFixture(t)
			a, e := m.reserveHierarchy(context.Background(), "admin", in)
			if e != nil {
				t.Fatal(e)
			}
			b, e := m.reserveHierarchy(context.Background(), "admin", in)
			if e != nil {
				a()
				t.Fatal(e)
			}
			defer b()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if scenario == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 70*time.Millisecond)
				defer cancel()
			}
			result := make(chan error, 1)
			go func() {
				r, e := m.reserveHierarchy(ctx, "admin", in)
				if e == nil {
					r()
				}
				result <- e
			}()
			// Observe real backlog registration; do not infer ownership from an arbitrary delay.
			until := time.Now().Add(time.Second)
			for {
				m.mu.Lock()
				waiting := len(m.hierarchyWaiters)
				m.mu.Unlock()
				if waiting == 1 {
					break
				}
				if time.Now().After(until) {
					a()
					t.Fatal("waiter never registered")
				}
				time.Sleep(time.Millisecond)
			}
			// Both control connections remain obtainable while the slot backlog is occupied.
			resourceCtx, resourceCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			c1, resourceErr := m.readDB.Conn(resourceCtx)
			if resourceErr != nil {
				resourceCancel()
				a()
				t.Fatal(resourceErr)
			}
			c2, resourceErr := m.readDB.Conn(resourceCtx)
			c1.Close()
			if c2 != nil {
				c2.Close()
			}
			resourceCancel()
			if resourceErr != nil {
				a()
				t.Fatal(resourceErr)
			}
			var want error
			switch scenario {
			case "manager-cancel":
				m.cancel()
				want = ErrUnavailable
			case "snapshot-rebound":
				m.readerMu.Lock()
				m.viewLeases[in.ViewID].Snapshot = "another-snapshot"
				m.readerMu.Unlock()
				want = query.ErrSnapshot
			case "cancel":
				cancel()
				want = ErrUnavailable
			case "deadline":
				want = context.DeadlineExceeded
			case "released":
				m.readerMu.Lock()
				m.viewLeases[in.ViewID].Released = true
				m.readerMu.Unlock()
				want = ErrViewExpired
			case "expired":
				m.readerMu.Lock()
				m.viewLeases[in.ViewID].Expires = time.Now().Add(-time.Second)
				m.readerMu.Unlock()
				want = ErrViewExpired
			case "profile-deleted":
				m.mu.Lock()
				_, e = m.db.Exec("DELETE FROM profiles WHERE id=?", in.ProfileID)
				m.mu.Unlock()
				if e != nil {
					a()
					t.Fatal(e)
				}
				want = ErrProfileMissing
			}
			if scenario != "deadline" {
				a()
				a = nil
			}
			e = <-result
			if a != nil {
				a()
			}
			if !errors.Is(e, want) {
				t.Fatalf("got %v, want %v", e, want)
			}
			m.mu.Lock()
			waiting := len(m.hierarchyWaiters)
			m.mu.Unlock()
			if waiting != 0 {
				t.Fatal("waiter leaked")
			}
		})
	}
}

// Exhausting the explicit backlog rejects excess requests while preserving two active slots.
func TestHierarchyAdmissionQueueBound(t *testing.T) {
	m, in := admissionFixture(t)
	a, e := m.reserveHierarchy(context.Background(), "admin", in)
	if e != nil {
		t.Fatal(e)
	}
	defer a()
	b, e := m.reserveHierarchy(context.Background(), "admin", in)
	if e != nil {
		t.Fatal(e)
	}
	defer b()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			r, e := m.reserveHierarchy(ctx, "admin", in)
			if e == nil {
				r()
			}
			results <- e
		}()
	}
	until := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		waiting := len(m.hierarchyWaiters)
		m.mu.Unlock()
		if waiting == 16 {
			break
		}
		if time.Now().After(until) {
			t.Fatal("backlog not filled")
		}
		time.Sleep(time.Millisecond)
	}
	if r, e := m.reserveHierarchy(context.Background(), "admin", in); !errors.Is(e, query.ErrBudget) {
		if r != nil {
			r()
		}
		t.Fatalf("excess admission: %v", e)
	}
	cancel()
	for i := 0; i < 16; i++ {
		if e := <-results; !errors.Is(e, ErrUnavailable) {
			t.Fatal(e)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.hierarchyWaiters) != 0 || len(m.hierarchySlots) != 2 {
		t.Fatal("reservation accounting")
	}
}

// Lifecycle contention must release scarce read connections before waiting for admission.
func TestHierarchyAdmissionLifecyclePoolIsolation(t *testing.T) {
	m, in := admissionFixture(t)
	waitQueryFixtureDispatch(t, m)
	var job string
	if err := m.readDB.QueryRow(`SELECT id FROM jobs WHERE json_extract(outcome_json,'$.job.resultSnapshotId')=?`, in.Query.SnapshotID).Scan(&job); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	locked := true
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	results := make(chan error, 2)
	defer func() {
		cancel()
		if locked {
			m.mu.Unlock()
		}
		for i := 0; i < 2; i++ {
			<-results
		}
	}()
	finished := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			release, err := m.reserveHierarchyAttempt(ctx, "admin", in)
			if release != nil {
				release()
			}
			results <- err
			finished <- struct{}{}
		}()
	}
	// Old code reaches two held connections; corrected attempts return before waiting.
	until := time.Now().Add(time.Second)
	for m.readDB.Stats().InUse != 2 && len(finished) != 2 {
		if time.Now().After(until) {
			t.Fatal("admission attempts did not reach the contention boundary")
		}
		time.Sleep(time.Millisecond)
	}
	readCtx, readCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err := m.ObserveJob(readCtx, job)
	readCancel()
	if err != nil {
		t.Fatalf("lifecycle admission starved independent job observation: %v", err)
	}
	// Both connections must also remain available while the lifecycle mutex stays held.
	poolCtx, poolCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer poolCancel()
	first, err := m.readDB.Conn(poolCtx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.readDB.Conn(poolCtx)
	first.Close()
	if second != nil {
		second.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Unlock()
	locked = false
	release, err := m.reserveHierarchy(ctx, "admin", in)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// Lifecycle retries preserve cancellation and requalify authority after the mutex becomes available.
func TestHierarchyLifecycleRetryRevalidation(t *testing.T) {
	for _, scenario := range []string{"success", "cancel", "deadline", "manager-cancel", "released", "expired", "profile-deleted", "snapshot-rebound"} {
		t.Run(scenario, func(t *testing.T) {
			m, in := admissionFixture(t)
			waitQueryFixtureDispatch(t, m)
			m.mu.Lock()
			locked := true
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if scenario == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
			}
			defer cancel()
			defer func() {
				if locked {
					m.mu.Unlock()
				}
			}()
			// Exercise the contended attempt directly before testing the public retry loop.
			if r, err := m.reserveHierarchyAttempt(ctx, "admin", in); r != nil || !errors.Is(err, errHierarchyLifecycleBusy) {
				t.Fatalf("contended admission: release=%v err=%v", r != nil, err)
			}
			result := make(chan error, 1)
			go func() {
				r, err := m.reserveHierarchy(ctx, "admin", in)
				if r != nil {
					r()
				}
				result <- err
			}()
			var want error
			switch scenario {
			case "cancel":
				cancel()
				want = ErrUnavailable
			case "deadline":
				want = context.DeadlineExceeded
			case "manager-cancel":
				m.cancel()
				want = ErrUnavailable
			case "released", "expired", "snapshot-rebound":
				m.readerMu.Lock()
				if scenario == "released" {
					m.viewLeases[in.ViewID].Released = true
					want = ErrViewExpired
				} else if scenario == "expired" {
					m.viewLeases[in.ViewID].Expires = time.Now().Add(-time.Second)
					want = ErrViewExpired
				} else {
					m.viewLeases[in.ViewID].Snapshot = "another-snapshot"
					want = query.ErrSnapshot
				}
				m.readerMu.Unlock()
			case "profile-deleted":
				if _, err := m.db.Exec("DELETE FROM profiles WHERE id=?", in.ProfileID); err != nil {
					t.Fatal(err)
				}
				want = ErrProfileMissing
			}
			if scenario != "deadline" && scenario != "cancel" && scenario != "manager-cancel" {
				m.mu.Unlock()
				locked = false
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("lifecycle retry did not terminate")
			}
			if !locked {
				m.mu.Lock()
				locked = true
			}
			m.readerMu.Lock()
			pins := m.generationReaders[in.Query.SnapshotID]
			m.readerMu.Unlock()
			if pins != 0 || len(m.hierarchySlots) != 0 || len(m.hierarchyWaiters) != 0 {
				t.Fatal("admission resources leaked")
			}
		})
	}
}
