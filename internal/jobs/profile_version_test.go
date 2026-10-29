package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestConfigurationChangeDuringRunningJob blocks a real job at its end-observation boundary.
func TestConfigurationChangeDuringRunningJob(t *testing.T) {
	m, in, source, catalog := nativeFixture(t, "volumes")
	ctx := context.Background()
	share := filepath.Join(filepath.Dir(source), "second-share")
	if err := os.Mkdir(share, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "inside"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	catalog.Shares = append(catalog.Shares, contracts.Share{ID: "share-a", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true})
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	m.catalog = func(context.Context, profileSources) (profileSources, error) {
		if calls.Add(1) == 2 {
			close(entered)
			<-release
		}
		return detachedCatalog(*catalog), nil
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real job did not reach end validation")
	}
	// Mutating the outward result cannot change the running worker or its private active record.
	started.Job.CapturedProfile.Scope.TargetIDs[0] = "foreign"
	started.ScopeManifest.Roots[0].Binding.Path = "/foreign"
	changed := in.Profile
	changed.Scope = contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}
	saved, err := m.UpdateProfile(ctx, in.Profile.ID, 1, changed)
	if err != nil || saved.Revision != 2 {
		t.Fatal(saved, err)
	}
	if _, err = m.UpdateProfile(ctx, in.Profile.ID, 1, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("stale writer accepted", err)
	}
	if _, err = m.Start(in.Profile.ID, "full"); !errors.Is(err, ErrConflict) {
		t.Fatal("new revision deduplicated into old job", err)
	}
	running, err := m.Get(started.Job.ID)
	if err != nil || running.Job.ProfileRevision != 1 || running.Job.CapturedProfile.Scope.Kind != "volumes" || running.ScopeManifest.Roots[0].Binding.Path != share {
		t.Fatal(running, err)
	}
	close(release)
	released = true
	done := terminal(t, m, started.Job.ID)
	if done.Job.State != "completed" || done.Job.ProfileRevision != 1 || done.Job.BytesObserved != "9" {
		t.Fatal(done)
	}
	view, err := m.Snapshot(ctx, in.Profile.ID)
	if err != nil || view.ProfileRevision == nil || *view.ProfileRevision != 1 || view.CurrentProfileRevision != 2 || !view.ProfileChanged || *view.CapturedProfileName != in.Profile.Name {
		t.Fatal(view, err)
	}
	if _, err = m.RenameProfile(ctx, in.Profile.ID, 2, "Renamed after change"); err != nil {
		t.Fatal(err)
	}
	var jobCount int
	m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobCount)
	if jobCount != 1 {
		t.Fatal("rename started a scan")
	}
	next, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	nextDone := terminal(t, m, next.Job.ID)
	if nextDone.Job.ProfileRevision != 2 || nextDone.Job.BytesObserved != "6" || nextDone.Job.CapturedProfile.Name != "Renamed after change" {
		t.Fatal(nextDone)
	}
	current, err := m.Snapshot(ctx, in.Profile.ID)
	if err != nil || current.ProfileChanged || *current.ProfileRevision != 2 {
		t.Fatal(current, err)
	}
	oldDB, err := index.Open(filepath.Join(m.state, "generations", *done.Job.ResultSnapshotID, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer oldDB.Close()
	header, err := index.ReadGenerationHeader(ctx, oldDB, *done.Job.ResultSnapshotID)
	if err != nil || header.Profile.Revision != 1 || header.Profile.Scope.Kind != "volumes" || header.Profile.Name != in.Profile.Name {
		t.Fatal(header, err)
	}
}

// TestOverlappingProfilesStaySeparate proves independent generations and snapshot-qualified numeric locators.
func TestOverlappingProfilesStaySeparate(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	first, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	one := terminal(t, m, first.Job.ID)
	in.Profile.ID = "other-profile"
	if err = m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	second, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	two := terminal(t, m, second.Job.ID)
	a, err := m.Snapshot(ctx, "probe")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Snapshot(ctx, "other-profile")
	if err != nil {
		t.Fatal(err)
	}
	if *a.SnapshotID != *one.Job.ResultSnapshotID || *b.SnapshotID != *two.Job.ResultSnapshotID || *a.SnapshotID == *b.SnapshotID || a.Nodes[0].SnapshotID != *a.SnapshotID || b.Nodes[0].SnapshotID != *b.SnapshotID || one.Job.BytesObserved != "3" || two.Job.BytesObserved != "3" {
		t.Fatal(a, b)
	}
}

// TestDetachedOutcomeIncludesExclusions protects future exclusion support without implementing its semantics.
func TestDetachedOutcomeIncludesExclusions(t *testing.T) {
	o := Outcome{Job: contracts.Job{BytesObserved: "0", CapturedProfile: contracts.Profile{Scope: contracts.Scope{TargetIDs: []string{"a"}}, Exclusions: []contracts.Exclusion{{Kind: "pattern", Value: "*.tmp"}}}}, ScopeManifest: &scopemanifest.ScopeManifest{Exclusions: []scopemanifest.FixedExclusion{{TargetID: "a", RelativePath: "state"}}}}
	before, _ := json.Marshal(o)
	copy, err := detachOutcome(o)
	if err != nil {
		t.Fatal(err)
	}
	copy.Job.CapturedProfile.Exclusions[0].Value = "*"
	copy.Job.CapturedProfile.Scope.TargetIDs[0] = "b"
	copy.ScopeManifest.Exclusions[0].RelativePath = "other"
	after, _ := json.Marshal(o)
	if string(before) != string(after) {
		t.Fatal("captured state aliased")
	}
}
