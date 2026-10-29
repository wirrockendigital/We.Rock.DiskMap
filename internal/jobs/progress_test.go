package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real scans distinguish files/directories/specials and preserve exact metadata values.
func TestProgressMeasuredAndEstimated(t *testing.T) {
	m, in, source := fixture(t)
	if err := os.Mkdir(filepath.Join(source, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data", filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	first, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, first.Job.ID)
	// Completion persistence precedes the worker's final optional presentation flush.
	var p JobProgress
	for i := 0; i < 100; i++ {
		p, err = m.Progress(context.Background(), first.Job.ID)
		if err == nil && p.Entries == "4" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil || !p.PublishedComplete || p.ExactTotal != nil || p.Estimate != nil || p.Files != "1" || p.Directories != "2" || p.Specials != "1" || p.KnownLogicalBytes != "3" || p.ElapsedSeconds == nil {
		t.Fatalf("%+v %v", p, err)
	}
	second, err := m.Start(in.Profile.ID, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, second.Job.ID)
	p, err = m.Progress(context.Background(), second.Job.ID)
	if err != nil || p.Estimate == nil || p.Estimate.Entries != "4" || p.Estimate.Basis != "previous-complete" || p.Trigger != "cli" {
		t.Fatalf("%+v %v", p, err)
	}
}

// Empty roots have a real root directory and no invented files or percentage denominator.
func TestProgressEmptyAndFailure(t *testing.T) {
	m, in, source := fixture(t)
	if err := os.Remove(filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	o, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, o.Job.ID)
	p, err := m.Progress(context.Background(), o.Job.ID)
	if err != nil || p.Files != "0" || p.Directories != "1" || p.KnownLogicalBytes != "0" {
		t.Fatalf("%+v %v", p, err)
	}
	m.spaceProbe = func() (uint64, error) { return 1, nil }
	o, err = m.Start(in.Profile.ID, "rebuild")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, o.Job.ID)
	p, err = m.Progress(context.Background(), o.Job.ID)
	if err != nil || p.State != "failed" || p.Measured || p.PublishedComplete || p.ExactTotal != nil || len(p.ErrorCodes) == 0 {
		t.Fatalf("%+v %v", p, err)
	}
}

// Samples remain exact above JS precision, include warnings and never expose private activity paths.
func TestProgressSampleAndHistoryBound(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	o, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, o.Job.ID)
	observer, finish := m.trackProgress(o.Outcome)
	observer(scanner.Progress{Entries: 9, Files: 4, Directories: 3, Specials: 2, UnknownLogical: 1, KnownLogicalBytes: "9007199254740993", Warnings: 3, TargetID: "share-one"})
	finish()
	p, err := m.Progress(context.Background(), o.Job.ID)
	if err != nil || p.KnownLogicalBytes != "9007199254740993" || p.Warnings != "3" || p.UnknownLogical != "1" {
		t.Fatalf("%+v %v", p, err)
	}
	m.mu.Lock()
	for i := 0; i < 1002; i++ {
		copy := o.Outcome
		copy.Job.ID = fmt.Sprintf("historic-%04d", i)
		copy.Job.State = "failed"
		copy.Job.Warnings = nil
		copy.Job.ResultSnapshotID = nil
		copy.SnapshotState = ""
		if e := m.save(copy); e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(progressRecord{Sample: scanner.Progress{KnownLogicalBytes: "0"}, At: now()})
		if _, e := m.db.Exec(`INSERT INTO job_progress VALUES(?,?)`, copy.Job.ID, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	m.mu.Unlock()
	// A subsequent tracker performs retention cleanup; finishing one tracker twice is idempotent.
	_, finishNext := m.trackProgress(o.Outcome)
	finishNext()
	h, err := m.History(context.Background(), in.Profile.ID)
	if err != nil || len(h.Jobs) != 50 || !h.Truncated {
		t.Fatalf("%+v %v", h, err)
	}
	var details, jobs int
	if e := m.db.QueryRow(`SELECT count(*) FROM job_progress`).Scan(&details); e != nil {
		t.Fatal(e)
	}
	_ = m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs)
	if details != 1000 || jobs != 1003 {
		t.Fatalf("details=%d jobs=%d", details, jobs)
	}
}

// A missing source is partial coverage, not a complete zero-byte NAS.
func TestProgressPartialSource(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, source+"-away"); err != nil {
		t.Fatal(err)
	}
	o, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, o.Job.ID)
	p, err := m.Progress(context.Background(), o.Job.ID)
	if err != nil || done.SnapshotState != "partial" || p.PublishedComplete || p.Warnings == "0" {
		t.Fatalf("%+v %+v %v", done, p, err)
	}
}

// The bounded history poll must seek by profile rather than scan all retained provenance.
func TestProgressHistoryQueryPlan(t *testing.T) {
	m, _, _ := fixture(t)
	rows, e := m.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=? ORDER BY rowid DESC LIMIT 51`, "probe")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if e = rows.Scan(&a, &b, &c, &detail); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(detail, "jobs_history_profile") {
			found = true
		}
		if strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal(detail)
		}
	}
	if !found {
		t.Fatal("history did not use profile index")
	}
}

// Recorded policies survive profile edits and restarts without reinterpreting older history.
func TestProgressResourcePolicyProvenance(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, started.Job.ID)
	// Stop the worker before editing the independent current profile and reopening persistence.
	state := m.state
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	in.Profile.Revision++
	in.Profile.ResourceMode = "fast"
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	p, err := m.Progress(context.Background(), started.Job.ID)
	if err != nil || p.ResourcePolicy == nil || p.ResourcePolicy.Mode != "gentle" || p.ResourcePolicy.PaceDelayMs != 8 || !p.ResourcePolicy.valid() {
		t.Fatalf("%+v %v", p, err)
	}
	// Removing only the optional historical sample must not reconstruct policy from captured inputs.
	if _, err := m.db.Exec(`UPDATE job_progress SET detail_json=json_remove(detail_json,'$.resourcePolicy') WHERE job_id=?`, started.Job.ID); err != nil {
		t.Fatal(err)
	}
	p, err = m.Progress(context.Background(), started.Job.ID)
	if err != nil || p.ResourcePolicy != nil {
		t.Fatalf("historical policy manufactured: %+v %v", p, err)
	}
	// Even a canonical preset cannot be attributed to a job that captured another mode.
	if _, err := m.db.Exec(`UPDATE job_progress SET detail_json=json_set(detail_json,'$.resourcePolicy',json('{"version":1,"mode":"fast","workers":1,"directoryBatch":128,"paceEvery":128,"paceDelayMs":0,"sqliteCacheKiB":8192,"tempCacheKiB":2048}')) WHERE job_id=?`, started.Job.ID); err != nil {
		t.Fatal(err)
	}
	p, err = m.Progress(context.Background(), started.Job.ID)
	if err != nil || p.ResourcePolicy != nil {
		t.Fatalf("foreign policy accepted: %+v %v", p, err)
	}
	// A malformed stored preset is unavailable rather than represented as an applied resource bound.
	if _, err := m.db.Exec(`UPDATE job_progress SET detail_json=json_set(detail_json,'$.resourcePolicy',json('{"version":1,"mode":"fast","workers":99}')) WHERE job_id=?`, started.Job.ID); err != nil {
		t.Fatal(err)
	}
	p, err = m.Progress(context.Background(), started.Job.ID)
	if err != nil || p.ResourcePolicy != nil {
		t.Fatalf("invalid policy accepted: %+v %v", p, err)
	}
}
