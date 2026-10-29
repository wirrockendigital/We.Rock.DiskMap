package jobs

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixture deliberately has no generation files: summary reads must remain metadata-only.
func summaryFixture(t *testing.T) *Manager {
	m := diagnosticFixture(t)
	for _, q := range []string{`DROP TABLE integrity_generations`, `CREATE TABLE integrity_generations(snapshot_id TEXT PRIMARY KEY,profile_id TEXT,quality TEXT,checked_at TEXT)`, `CREATE TABLE profiles(id TEXT PRIMARY KEY,revision INTEGER,profile_json TEXT)`, `CREATE TABLE active_generations(profile_id TEXT PRIMARY KEY,snapshot_id TEXT)`} {
		if _, e := m.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	p := contracts.Profile{ID: "p", Revision: 2, FreshnessSeconds: 3600}
	raw, _ := json.Marshal(p)
	if _, e := m.db.Exec(`INSERT INTO profiles VALUES('p',2,?)`, string(raw)); e != nil {
		t.Fatal(e)
	}
	return m
}
func summaryOutcome(t *testing.T, m *Manager, id, state, snapshot string, revision int) {
	t.Helper()
	end := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	o := Outcome{Job: contracts.Job{BytesObserved: "0", ID: id, ProfileID: "p", ProfileRevision: revision, State: state, ScanMode: "reconcile", StartedAt: &end, EndedAt: &end}, SnapshotState: "complete", SnapshotEndedAt: &end}
	if snapshot != "" {
		o.Job.ResultSnapshotID = &snapshot
	}
	if state != "completed" {
		o.SnapshotState = ""
	}
	raw, marshalErr := json.Marshal(o)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if _, e := m.db.Exec(`INSERT INTO jobs VALUES(?,?,?)`, id, state, string(raw)); e != nil {
		t.Fatal(e)
	}
}

// A newer failure never replaces the complete pointer or its captured revision.
func TestProfileSummariesRecordedRevisionAndLatestAttempt(t *testing.T) {
	m := summaryFixture(t)
	summaryOutcome(t, m, "old", "completed", "s", 1)
	summaryOutcome(t, m, "last", "failed", "", 2)
	m.db.Exec(`INSERT INTO active_generations VALUES('p','s')`)
	m.mu.Lock()
	defer m.mu.Unlock()
	out, e := m.ProfileSummaries(context.Background(), ProfileSummariesRequest{1, []string{"p"}})
	if e != nil {
		t.Fatal(e)
	}
	s := out.Profiles[0]
	if s.Quality != "complete" || s.Freshness != "stale" || *s.CapturedRevision != 1 || s.LastAttempt.State != "failed" || s.IntegrityQuality != "unknown" || s.AgeSeconds == nil {
		t.Fatal(s)
	}
	if !ValidProfileSummaries(out, ProfileSummariesRequest{1, []string{"p"}}) {
		t.Fatal("invalid output")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "rootPath") {
		t.Fatal("private projection")
	}
}

// Known integrity observations are reported honestly, never inferred from publication success.
func TestProfileSummariesMissingCorruptAndHistoricalTime(t *testing.T) {
	m := summaryFixture(t)
	req := ProfileSummariesRequest{1, []string{"p"}}
	out, e := m.ProfileSummaries(context.Background(), req)
	if e != nil || out.Profiles[0].Quality != "missing" || out.Profiles[0].LastAttempt != nil {
		t.Fatal(out, e)
	}
	summaryOutcome(t, m, "j", "completed", "s", 2)
	m.db.Exec(`INSERT INTO active_generations VALUES('p','s')`)
	m.db.Exec(`UPDATE jobs SET outcome_json=json_remove(outcome_json,'$.snapshotEndedAt')`)
	out, e = m.ProfileSummaries(context.Background(), req)
	if e != nil || out.Profiles[0].EndedAt != nil || out.Profiles[0].Freshness != "unknown" {
		t.Fatal(out, e)
	}
	m.db.Exec(`INSERT INTO integrity_generations VALUES('s','p','corrupt',NULL)`)
	out, e = m.ProfileSummaries(context.Background(), req)
	if e != nil || out.Profiles[0].Quality != "corrupt" {
		t.Fatal(out, e)
	}
}

// Invalid batches, missing profiles, cancelled SQL and corrupt metadata fail without partial publication.
func TestProfileSummariesBoundsAndFailures(t *testing.T) {
	m := summaryFixture(t)
	for _, in := range []ProfileSummariesRequest{{1, nil}, {1, []string{"p", "p"}}, {1, make([]string, 33)}, {2, []string{"p"}}} {
		if _, e := m.ProfileSummaries(context.Background(), in); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	if _, e := m.ProfileSummaries(context.Background(), ProfileSummariesRequest{1, []string{"p", "gone"}}); e != ErrNotFound {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.ProfileSummaries(ctx, ProfileSummariesRequest{1, []string{"p"}}); e != ErrUnavailable {
		t.Fatal(e)
	}
	m.db.Exec(`UPDATE profiles SET profile_json='broken'`)
	if _, e := m.ProfileSummaries(context.Background(), ProfileSummariesRequest{1, []string{"p"}}); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// All 32 requested identities preserve order; unavailable latest metadata is not silently skipped.
func TestProfileSummariesBatchAndCorruptLatest(t *testing.T) {
	m := summaryFixture(t)
	ids := []string{"p"}
	for n := 1; n < 32; n++ {
		id := "profile-" + strconv.Itoa(n)
		ids = append(ids, id)
		p := contracts.Profile{ID: id, Revision: 1, FreshnessSeconds: 3600}
		raw, e := json.Marshal(p)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = m.db.Exec(`INSERT INTO profiles VALUES(?,1,?)`, id, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	req := ProfileSummariesRequest{1, ids}
	out, e := m.ProfileSummaries(context.Background(), req)
	if e != nil || !ValidProfileSummaries(out, req) {
		t.Fatal(out, e)
	}
	summaryOutcome(t, m, "j", "completed", "s", 2)
	m.db.Exec(`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.id','mismatched')`)
	if _, e = m.ProfileSummaries(context.Background(), req); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// A blocked control pool respects cancellation without needing the lifecycle lock.
func TestProfileSummariesPoolDeadline(t *testing.T) {
	m := summaryFixture(t)
	conn, e := m.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e = m.ProfileSummaries(ctx, ProfileSummariesRequest{1, []string{"p"}}); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// Direct validator callers receive the same canonical decimal and provenance guarantees as JSON callers.
func TestProfileSummariesRejectContradictoryFreshness(t *testing.T) {
	m := summaryFixture(t)
	req := ProfileSummariesRequest{1, []string{"p"}}
	out, e := m.ProfileSummaries(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	for _, age := range []contracts.Bytes{"1", "01", "+1"} {
		copy := out
		copy.Profiles = append([]ProfileSummary(nil), out.Profiles...)
		copy.Profiles[0].AgeSeconds = &age
		if ValidProfileSummaries(copy, req) {
			t.Fatal("accepted missing age provenance", age)
		}
	}
	summaryOutcome(t, m, "j", "completed", "s", 2)
	m.db.Exec(`INSERT INTO active_generations VALUES('p','s')`)
	out, e = m.ProfileSummaries(context.Background(), req)
	if e != nil {
		t.Fatal(e)
	}
	for _, age := range []contracts.Bytes{"01", "+1"} {
		out.Profiles[0].AgeSeconds = &age
		if ValidProfileSummaries(out, req) {
			t.Fatal("accepted noncanonical age", age)
		}
	}
}
