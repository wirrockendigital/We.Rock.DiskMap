package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"os"
	"testing"
	"time"
)

// TestSizeMetricsLeaseBoundary proves size reads survive offline sources and cannot escape an exact reader.
func TestSizeMetricsLeaseBoundary(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, s.Job.ID)
	ctx := context.Background()
	view, err := m.OpenView(ctx, "admin", in.Profile.ID, "size-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	request := SizeMetricsRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas"}
	if err = os.Rename(source, source+"-away"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(source+"-away", source) })
	var before int
	if err = m.db.QueryRow("SELECT count(*) FROM jobs").Scan(&before); err != nil {
		t.Fatal(err)
	}
	out, err := m.ReadSizeMetrics(ctx, "admin", request)
	if err != nil || out.SnapshotID != request.SnapshotID || out.DefaultMetric != "logical" || out.Volume != nil {
		t.Fatal(out, err)
	}
	var after int
	if err = m.db.QueryRow("SELECT count(*) FROM jobs").Scan(&after); err != nil || before != after {
		t.Fatal("size read created a job", err)
	}
	if _, err = m.ReadSizeMetrics(ctx, "other", request); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign principal", err)
	}
	changed := request
	changed.SnapshotID = "other-snapshot"
	if _, err = m.ReadSizeMetrics(ctx, "admin", changed); !errors.Is(err, query.ErrSnapshot) {
		t.Fatal("snapshot substitution", err)
	}
	a, err := m.reserveHierarchy(ctx, "admin", request.hierarchy())
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.reserveHierarchy(ctx, "admin", request.hierarchy())
	if err != nil {
		a()
		t.Fatal(err)
	}
	// Size qualification shares the bounded admission wait and preserves caller deadlines.
	bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, err = m.ReadSizeMetrics(bounded, "admin", request)
	cancel()
	a()
	b()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unbounded reads", err)
	}
	now := time.Now().Add(viewTTL + time.Minute)
	m.viewClock = func() time.Time { return now }
	if _, err = m.ReadSizeMetrics(ctx, "admin", request); !errors.Is(err, ErrViewExpired) {
		t.Fatal("expired reader", err)
	}
}
