package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestExpiredLeaseBypassesContendedReservation holds real locks/connections rather than mocking SQL latency.
func TestExpiredLeaseBypassesContendedReservation(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	view, err := m.OpenView(context.Background(), "admin", "probe", "expiry-test", "", false)
	if err != nil {
		t.Fatal(err)
	}
	request := HierarchyRequest{ContractVersion: 1, ProfileID: "probe", ViewID: "expiry-test", Query: query.Request{SnapshotID: *view.View.SnapshotID, RootID: "root", Metric: "logical", Depth: 1, ElementBudget: 1}}
	m.readerMu.Lock()
	m.viewLeases[request.ViewID].Expires = time.Now().Add(-time.Second)
	m.readerMu.Unlock()
	for _, held := range []string{"lifecycle", "control-pool"} {
		t.Run(held, func(t *testing.T) {
			unlock := func() {}
			if held == "lifecycle" {
				m.mu.Lock()
				unlock = m.mu.Unlock
			} else {
				m.db.SetMaxOpenConns(1)
				conn, err := m.db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				unlock = func() { conn.Close() }
			}
			result := make(chan error, 1)
			go func() {
				release, err := m.reserveHierarchy(context.Background(), "admin", request)
				if release != nil {
					release()
				}
				result <- err
			}()
			select {
			case err := <-result:
				unlock()
				if !errors.Is(err, ErrViewExpired) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				unlock()
				<-result
				t.Fatal("expired lease waited for unrelated reservation resources")
			}
		})
	}
	// Only the exact old binding receives early denial; normal error precedence remains for foreign selectors.
	for _, tc := range []struct {
		owner, profile, snapshot, view string
		want                           error
	}{{"other", "probe", request.Query.SnapshotID, request.ViewID, ErrViewExpired}, {"admin", "missing", request.Query.SnapshotID, request.ViewID, ErrProfileMissing}, {"admin", "probe", "other", request.ViewID, ErrViewExpired}, {"admin", "probe", request.Query.SnapshotID, "missing", ErrViewExpired}} {
		changed := request
		changed.ProfileID = tc.profile
		changed.ViewID = tc.view
		changed.Query.SnapshotID = tc.snapshot
		if _, err := m.reserveHierarchy(context.Background(), tc.owner, changed); !errors.Is(err, tc.want) {
			t.Fatalf("precedence: %v want %v", err, tc.want)
		}
	}
	// Live views retain the existing owner/snapshot boundary and reserve a real reader reference.
	m.readerMu.Lock()
	m.viewLeases[request.ViewID].Expires = time.Now().Add(time.Minute)
	m.readerMu.Unlock()
	if _, err := m.reserveHierarchy(context.Background(), "other", request); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	changed := request
	changed.Query.SnapshotID = "other"
	if _, err := m.reserveHierarchy(context.Background(), "admin", changed); !errors.Is(err, query.ErrSnapshot) {
		t.Fatal(err)
	}
	release, err := m.reserveHierarchy(context.Background(), "admin", request)
	if err != nil {
		t.Fatal(err)
	}
	release()
	// A matching released view is already unusable even if its profile was subsequently removed.
	m.readerMu.Lock()
	m.viewLeases[request.ViewID].Released = true
	m.readerMu.Unlock()
	if _, err = m.db.Exec(`DELETE FROM profiles WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.reserveHierarchy(context.Background(), "admin", request); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
	changed = request
	changed.Query.SnapshotID = "other"
	if _, err = m.reserveHierarchy(context.Background(), "admin", changed); !errors.Is(err, ErrProfileMissing) {
		t.Fatal(err)
	}
}
