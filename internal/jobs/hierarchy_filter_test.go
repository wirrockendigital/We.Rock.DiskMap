package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestFilteredHierarchyLeaseBoundary checks asynchronous filtering against a pinned, source-independent snapshot.
func TestFilteredHierarchyLeaseBoundary(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, started.Job.ID)
	view, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "filter-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	req := HierarchyRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, Query: query.Request{SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas", Metric: "logical", Depth: 3, ElementBudget: 100, Filter: query.Filter{Text: "data", Field: "name"}}}
	// Queries must not touch live source paths, even while their cache is being built.
	if err = os.Rename(source, source+"-filter-away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(source+"-filter-away", source) })
	first, err := m.ReadHierarchy(context.Background(), "admin", req, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var pending map[string]json.RawMessage
	if err = json.Unmarshal(raw, &pending); err != nil || string(pending["state"]) != `"pending"` || pending["summary"] != nil {
		t.Fatal(string(raw), err)
	}
	// Another principal cannot start or reuse a projection through this view lease.
	if _, err = m.ReadHierarchy(context.Background(), "other", req, false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, e := m.ReadHierarchy(context.Background(), "admin", req, false)
		if e != nil {
			t.Fatal(e)
		}
		if ready, ok := got.(query.Response); ok {
			if ready.SnapshotID != req.Query.SnapshotID || ready.MatchState != "matches" {
				t.Fatal(ready)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("filter did not reach ready state")
}
