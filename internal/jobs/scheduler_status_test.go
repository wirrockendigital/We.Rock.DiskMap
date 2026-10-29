package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A real published scan remains discoverable beyond fifty later failures, even with sources offline.
func TestSchedulerStatusHistoricalSuccess(t *testing.T) {
	m, in, source := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	o, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	complete := terminal(t, m, o.Job.ID)
	m.mu.Lock()
	for i := 0; i < 60; i++ {
		failed := complete
		failed.Job.ID = fmt.Sprintf("failed%d", i)
		failed.Job.State = "failed"
		failed.Job.Trigger = "ui"
		failed.Job.ResultSnapshotID = nil
		failed.SnapshotState = ""
		raw, _ := json.Marshal(failed)
		if _, e = m.db.Exec("INSERT INTO jobs VALUES(?,?,?)", failed.Job.ID, "failed", string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	m.mu.Unlock()
	if e = os.Rename(source, source+"-offline"); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(source+"-offline", source)
	status, e := m.SchedulerStatus(context.Background(), in.Profile.ID)
	if e != nil || status.LastSuccessfulScan == nil || status.LastSuccessfulScan.JobID != complete.Job.ID || status.LastLocalCall == nil || status.LastLocalCall.JobID != complete.Job.ID {
		t.Fatalf("%+v %v", status, e)
	}
	if status.Schedule.Capability != "unavailable" || status.Schedule.Exists != nil || status.Schedule.Enabled != nil || status.Schedule.NextRun != nil {
		t.Fatal("invented DSM state")
	}
	// Index retention does not erase the accurately labelled historical publication event.
	if e = os.Rename(filepath.Join(m.state, "generations", *complete.Job.ResultSnapshotID), filepath.Join(m.state, "retained-away")); e != nil {
		t.Fatal(e)
	}
	if status, e = m.SchedulerStatus(context.Background(), in.Profile.ID); e != nil || status.LastSuccessfulScan == nil {
		t.Fatal("history incorrectly requires current index", e)
	}
}

// Inactive profiles retain observations; removed IDs never resolve by a new profile's matching name.
func TestSchedulerStatusIdentityAndErrors(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	empty, e := m.SchedulerStatus(context.Background(), in.Profile.ID)
	if e != nil || empty.LastSuccessfulScan != nil || empty.LastLocalCall != nil {
		t.Fatal(empty, e)
	}
	m.mu.Lock()
	_, e = m.db.Exec(`UPDATE profiles SET profile_json=json_set(profile_json,'$.active',json('false')) WHERE id=?`, in.Profile.ID)
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.SchedulerStatus(context.Background(), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, e = m.SchedulerStatus(ctx, in.Profile.ID); e == nil {
		t.Fatal("timeout misreported as empty history")
	}
	for _, id := range []string{"../escape", "", "x?enabled=true"} {
		if _, e = m.SchedulerStatus(context.Background(), id); e != ErrInvalid {
			t.Fatal("invalid ID accepted")
		}
	}
	m.mu.Lock()
	_, e = m.db.Exec("DELETE FROM profiles WHERE id=?", in.Profile.ID)
	m.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	in.Profile.ID = "imported-new-id"
	if e = m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	if _, e = m.SchedulerStatus(context.Background(), "probe"); e != ErrNotFound {
		t.Fatalf("deleted ID not rejected: %v", e)
	}
	if _, e = m.SchedulerStatus(context.Background(), in.Profile.ID); e != nil {
		t.Fatal(e)
	}
}
