package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// statusFixture isolates the read contract from scanner I/O and asynchronous worker scheduling.
func statusFixture(t *testing.T) *Manager {
	t.Helper()
	db, e := index.Open(filepath.Join(t.TempDir(), "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`CREATE TABLE jobs(id TEXT PRIMARY KEY,state TEXT NOT NULL,outcome_json TEXT NOT NULL); PRAGMA user_version=6`); e != nil {
		t.Fatal(e)
	}
	if e = initializeQueue(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if e = initializeStatusSnapshot(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	return &Manager{db: db, readDB: db, ctx: context.Background()}
}

// statusInsert writes the same captured identity fields as real acceptance and claim transactions.
func statusInsert(t *testing.T, m *Manager, i int, state string) {
	t.Helper()
	id := fmt.Sprintf("j%d", i)
	p := fmt.Sprintf("p%d", i)
	at := "2026-10-10T00:00:00Z"
	j := contracts.Job{ID: id, ProfileID: p, ProfileRevision: 1, CapturedProfile: contracts.Profile{ID: p, Revision: 1}, ScanMode: "full", Trigger: "cli", State: state, CreatedAt: at, BytesObserved: "0", EntriesVisited: "0"}
	if state != "queued" {
		j.StartedAt = &at
	}
	raw, _ := json.Marshal(Outcome{Job: j})
	if _, e := m.db.Exec(`INSERT INTO jobs VALUES(?,?,?)`, id, state, string(raw)); e != nil {
		t.Fatal(e)
	}
	if state == "queued" {
		if _, e := m.db.Exec(`INSERT INTO scan_queue(job_id,profile_id,sources_json) VALUES(?,?,'{}')`, id, p); e != nil {
			t.Fatal(e)
		}
	}
}

// TestStatusSnapshotBounds proves dispatch transients, cardinality and corruption fail closed.
func TestStatusSnapshotBounds(t *testing.T) {
	m := statusFixture(t)
	v, e := m.Status(context.Background())
	if e != nil || v.Active != nil || v.Queued == nil || len(v.Queued) != 0 {
		t.Fatal(v, e)
	}
	for i := 0; i < 17; i++ {
		statusInsert(t, m, i, "queued")
	}
	v, e = m.Status(context.Background())
	if e != nil || len(v.Queued) != 17 || v.Active != nil {
		t.Fatal(v, e)
	}
	for i, j := range v.Queued {
		if j.JobID != fmt.Sprintf("j%d", i) || j.QueuePosition != i+1 || j.Trigger != "cli" {
			t.Fatal(j)
		}
	}
	statusInsert(t, m, 17, "preflight")
	if _, e = m.Status(context.Background()); e != ErrUnavailable {
		t.Fatal("overflow", e)
	}
	if _, e = m.db.Exec(`DELETE FROM jobs WHERE id='j17'; DELETE FROM scan_queue WHERE job_id='j0'`); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Status(context.Background()); e != ErrUnavailable {
		t.Fatal("orphan queued outcome", e)
	}
}

// TestStatusSnapshotRealQueue checks the existing dispatcher, immutable revision and no lifecycle lock.
func TestStatusSnapshotRealQueue(t *testing.T) {
	m, _ := queueFixture(t, 3)
	release := holdFirstQueue(t, m)
	defer release()
	for i := 1; i < 3; i++ {
		if _, e := m.StartBrowser(fmt.Sprintf("queue-%02d", i), "reconcile", BrowserStartIdentity{"admin", fmt.Sprintf("status-%d", i)}); e != nil {
			t.Fatal(e)
		}
	}
	m.mu.Lock()
	v, e := m.Status(context.Background())
	m.mu.Unlock()
	if e != nil || v.Active == nil || v.Active.State != "preflight" || v.Active.Trigger != "cli" || len(v.Queued) != 2 {
		t.Fatal(v, e)
	}
	for _, q := range v.Queued {
		if q.Trigger != "ui" || q.ScanMode != "reconcile" || q.ProfileRevision != 1 {
			t.Fatal(q)
		}
	}
	release()
	terminal(t, m, v.Queued[1].JobID)
	v, e = m.Status(context.Background())
	if e != nil || v.Active != nil || len(v.Queued) != 0 {
		t.Fatal(v, e)
	}
}

// TestStatusSnapshotAtomicClaim repeatedly reads while claim and rollback are committed atomically.
func TestStatusSnapshotAtomicClaim(t *testing.T) {
	m := statusFixture(t)
	statusInsert(t, m, 0, "queued")
	var raw string
	if e := m.db.QueryRow(`SELECT outcome_json FROM jobs`).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var o Outcome
	json.Unmarshal([]byte(raw), &o)
	o.Job.State = "preflight"
	o.Job.StartedAt = &o.Job.CreatedAt
	active, _ := json.Marshal(o)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 30; i++ {
			tx, e := m.db.Begin()
			if e != nil {
				done <- e
				return
			}
			_, e = tx.Exec(`DELETE FROM scan_queue; UPDATE jobs SET state='preflight',outcome_json=?`, string(active))
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			if e != nil {
				done <- e
				return
			}
			tx, e = m.db.Begin()
			if e != nil {
				done <- e
				return
			}
			_, e = tx.Exec(`UPDATE jobs SET state='queued',outcome_json=?; INSERT INTO scan_queue(job_id,profile_id,sources_json) VALUES('j0','p0','{}')`, raw)
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 30; i++ {
		v, e := m.Status(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		n := len(v.Queued)
		if v.Active != nil {
			n++
		}
		if n != 1 {
			t.Fatal(v)
		}
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

// TestStatusSnapshotIndexAndFailure proves indexed reads, idempotent legacy initialization and no false empty result.
func TestStatusSnapshotIndexAndFailure(t *testing.T) {
	m := statusFixture(t)
	if _, e := m.db.Exec(`DROP INDEX jobs_status_live`); e != nil {
		t.Fatal(e)
	}
	if e := initializeStatusSnapshot(context.Background(), m.db); e != nil {
		t.Fatal(e)
	}
	if e := initializeStatusSnapshot(context.Background(), m.db); e != nil {
		t.Fatal(e)
	}
	var version int
	m.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != 6 {
		t.Fatal(version)
	}
	rows, e := m.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM jobs INDEXED BY jobs_status_live WHERE ` + liveStatusPredicate + ` ORDER BY state,id LIMIT 18`)
	if e != nil {
		t.Fatal(e)
	}
	plan := ""
	for rows.Next() {
		var a, b, c int
		var d string
		rows.Scan(&a, &b, &c, &d)
		plan += d
	}
	rows.Close()
	if !strings.Contains(plan, "jobs_status_live") || strings.Contains(plan, "TEMP") {
		t.Fatal(plan)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.Status(ctx); e != ErrUnavailable {
		t.Fatal(e)
	}
	statusInsert(t, m, 0, "preflight")
	statusInsert(t, m, 1, "scanning")
	if _, e = m.Status(context.Background()); e != ErrUnavailable {
		t.Fatal("two active", e)
	}
}

// TestStatusSnapshotPublicValidation rejects private fields indirectly through the narrow exported type and semantic inconsistencies.
func TestStatusSnapshotPublicValidation(t *testing.T) {
	m := statusFixture(t)
	statusInsert(t, m, 0, "queued")
	v, e := m.Status(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(v)
	for _, s := range []string{"capturedProfile", "scopeManifest", "sources_json", "rootPath"} {
		if strings.Contains(string(raw), s) {
			t.Fatal(s)
		}
	}
	v.Queued[0].StartedAt = &v.ObservedAt
	if ValidStatusSnapshot(v) {
		t.Fatal("queued started")
	}
	v.Queued[0].StartedAt = nil
	v.Queued[0].QueuePosition = 0
	if ValidStatusSnapshot(v) {
		t.Fatal("position")
	}
	v.Queued[0].QueuePosition = 1
	v.ObservedAt = time.Now().Format("Monday")
	if ValidStatusSnapshot(v) {
		t.Fatal("time")
	}
}

// TestStatusSnapshotReopenExistingControl proves the additive index is installed on old Control6 without rewriting profile data.
func TestStatusSnapshotReopenExistingControl(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	var before string
	if e := m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id=?`, in.Profile.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	state := m.state
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	db, e := index.Open(filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DROP INDEX jobs_status_live`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	reopened, e := Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var after string
	var version int
	if e = reopened.db.QueryRow(`SELECT profile_json FROM profiles WHERE id=?`, in.Profile.ID).Scan(&after); e != nil {
		t.Fatal(e)
	}
	if e = reopened.db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil || version != migration.ControlVersion || before != after {
		t.Fatal(version, e)
	}
	if _, e = reopened.Status(context.Background()); e != nil {
		t.Fatal(e)
	}
}

// TestStatusSnapshotCorruption rejects queue references to terminal or missing jobs and bad stored identities.
func TestStatusSnapshotCorruption(t *testing.T) {
	for _, statement := range []string{
		`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.id','foreign')`,
		`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.capturedProfile.revision',2)`,
		`UPDATE jobs SET state='completed'`,
		`DELETE FROM jobs`,
		`UPDATE scan_queue SET profile_id='foreign'`,
		`UPDATE jobs SET outcome_json='broken'`,
	} {
		t.Run(statement, func(t *testing.T) {
			m := statusFixture(t)
			statusInsert(t, m, 0, "queued")
			if _, e := m.db.Exec(statement); e != nil {
				t.Fatal(e)
			}
			if _, e := m.Status(context.Background()); e != ErrUnavailable {
				t.Fatal(e)
			}
		})
	}
}

// Private metadata size does not constrain the small status contract; selected scalar bounds do.
func TestStatusSnapshotLargePrivateProjection(t *testing.T) {
	m := statusFixture(t)
	statusInsert(t, m, 0, "queued")
	// Model a large captured manifest plus its independent bounded provenance block.
	// Status validates job identity, not the private scanner authority's contents.
	private := strings.Repeat("x", 700000)
	if _, e := m.db.Exec(`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.scopeManifest',json_object('provenance',json_object('version',1,'authority','synthetic-fixture','roots',json_array()),'privateMetadata',?),'$.job.capturedProfile.privateMetadata',?)`, private, private); e != nil {
		t.Fatal(e)
	}
	var size int
	if e := m.db.QueryRow(`SELECT length(CAST(outcome_json AS BLOB)) FROM jobs`).Scan(&size); e != nil || size <= 1<<20 {
		t.Fatal(size, e)
	}
	out, e := m.Status(context.Background())
	if e != nil || len(out.Queued) != 1 || out.Queued[0].JobID != "j0" {
		t.Fatal(out, e)
	}
	raw, _ := json.Marshal(out)
	if len(raw) > 1000 || strings.Contains(string(raw), "privateMetadata") {
		t.Fatal("private projection leaked")
	}
	for _, tc := range []struct {
		path  string
		value any
	}{
		{"$.job.profileId", strings.Repeat("p", 129)},
		{"$.job.createdAt", strings.Repeat("x", 70000)},
		{"$.job.trigger", strings.Repeat("x", 70000)},
		{"$.job.startedAt", strings.Repeat("x", 70000)},
		{"$.job.profileRevision", "1"},
		{"$.job.startedAt", 3},
	} {
		t.Run(tc.path+fmt.Sprint(tc.value)[:1], func(t *testing.T) {
			tx, e := m.db.Begin()
			if e != nil {
				t.Fatal(e)
			}
			var original string
			if e = tx.QueryRow(`SELECT outcome_json FROM jobs`).Scan(&original); e != nil {
				t.Fatal(e)
			}
			if _, e = tx.Exec(`UPDATE jobs SET outcome_json=json_set(outcome_json,?,?)`, tc.path, tc.value); e != nil {
				t.Fatal(e)
			}
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
			if _, e = m.Status(context.Background()); e != ErrUnavailable {
				t.Fatal("accepted malformed status scalar", e)
			}
			if _, e = m.db.Exec(`UPDATE jobs SET outcome_json=?`, original); e != nil {
				t.Fatal(e)
			}
		})
	}
}
