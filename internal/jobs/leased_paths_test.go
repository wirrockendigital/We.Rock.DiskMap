package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestLeasedPathsHistoricalNavigation keeps a real sealed generation readable without source access.
func TestLeasedPathsHistoricalNavigation(t *testing.T) {
	m, in, source := fixture(t)
	name := "Grüße %2F # <file>.txt"
	if err := os.WriteFile(filepath.Join(source, name), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	first := terminal(t, m, run.Job.ID)
	ctx := context.Background()
	view, err := m.OpenView(ctx, "admin", in.Profile.ID, "paths-view", "", false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(ctx, in.Profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	var node string
	for _, n := range snapshot.Nodes {
		if n.OriginalName == name {
			node = n.ID
		}
	}
	if node == "" {
		t.Fatal("fixture missing")
	}
	request := LeasedPathsRequest{1, in.Profile.ID, view.Lease.ID, *snapshot.SnapshotID, node}
	// Publication and mutable revision changes cannot retarget a historical lease.
	if err = os.Remove(filepath.Join(source, name)); err != nil {
		t.Fatal(err)
	}
	run, err = m.Start(in.Profile.ID, "rebuild")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	if _, err = m.db.Exec(`UPDATE profiles SET revision=revision+1, roots_json='{}' WHERE id=?`, in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(source+"-offline", source) })
	var before, after int
	_ = m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&before)
	samples := make([]time.Duration, 300)
	for i := range samples {
		start := time.Now()
		out, e := m.ReadLeasedPaths(ctx, "admin", request)
		samples[i] = time.Since(start)
		if e != nil || out.Name.Text != name || out.NASPath.Text != filepath.Join(source, name) || out.FileStationPath != nil {
			t.Fatal(out, e)
		}
	}
	_ = m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&after)
	if before != after {
		t.Fatal("navigation created scans")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	t.Logf("300 offline-source navigations; p95=%s (local, not native G07)", samples[284])
	if _, err = m.ReadLeasedPaths(ctx, "other", request); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err = m.cleanupGeneration(ctx, in.Profile.ID, request.SnapshotID, first.Job.ID, ""); !errors.Is(err, ErrConflict) {
		t.Fatal("leased cleanup was allowed", err)
	}
	changed := request
	changed.SnapshotID = "other"
	if _, err = m.ReadLeasedPaths(ctx, "admin", changed); !errors.Is(err, query.ErrSnapshot) {
		t.Fatal(err)
	}
	changed = request
	changed.NodeID = "unknown"
	if _, err = m.ReadLeasedPaths(ctx, "admin", changed); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = m.ReadLeasedPaths(cancelled, "admin", request); err == nil {
		t.Fatal("cancel ignored")
	}
	now := time.Now().Add(viewTTL + time.Minute)
	m.viewClock = func() time.Time { return now }
	if _, err = m.ReadLeasedPaths(ctx, "admin", request); !errors.Is(err, ErrViewExpired) {
		t.Fatal(err)
	}
}

// TestLeasedPathsPartialMetadata reads the surviving scope of a deliberately partial result.
func TestLeasedPathsPartialMetadata(t *testing.T) {
	m, in, source := fixture(t)
	missing := filepath.Join(filepath.Dir(source), "missing")
	if err := os.Mkdir(missing, 0700); err != nil {
		t.Fatal(err)
	}
	in.Profile.Scope.TargetIDs = append(in.Profile.Scope.TargetIDs, "share-two")
	in.Roots["share-two"] = missing
	in.RootKinds["share-two"] = "share"
	in.VolumeIDs["share-two"] = "volume-two"
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, run.Job.ID)
	if done.SnapshotState != "partial" {
		t.Fatal(done)
	}
	view, err := m.OpenView(context.Background(), "admin", in.Profile.ID, "partial-paths", done.Job.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, n := range view.View.Nodes {
		if n.OriginalName == "data" {
			id = n.ID
		}
	}
	if id == "" {
		t.Fatal("surviving node missing")
	}
	out, err := m.ReadLeasedPaths(context.Background(), "admin", LeasedPathsRequest{1, in.Profile.ID, view.Lease.ID, *done.Job.ResultSnapshotID, id})
	if err != nil || out.NASPath.Text != filepath.Join(source, "data") || out.FileStationPath != nil {
		t.Fatal(out, err)
	}
	// Partial coverage elsewhere must not disable a currently verified surviving file.
	request := LeasedPathsRequest{1, in.Profile.ID, view.Lease.ID, *done.Job.ResultSnapshotID, id}
	shares := map[string]string{"Share": source}
	target, err := m.ReadLeasedFileStation(context.Background(), "admin", request, shares)
	if err != nil || target.FileStationPath != "/Share/data" {
		t.Fatal(target, err)
	}
	if _, err = m.ReadLeasedFileStation(context.Background(), "foreign", request, shares); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// A replacement with the same name still must not inherit the recorded object's authority.
	if err = os.Rename(filepath.Join(source, "data"), filepath.Join(source, "original")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "data"), []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadLeasedFileStation(context.Background(), "admin", request, shares); !errors.Is(err, ErrFileStationUnavailable) {
		t.Fatal(err)
	}
}
