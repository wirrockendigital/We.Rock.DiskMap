package jobs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// observedFixture supplies genuine terminal capture and samples rather than mocked status rows.
func observedFixture(t *testing.T) (*Manager, string, string) {
	t.Helper()
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	sub, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, sub.Job.ID)
	return m, in.Profile.ID, sub.Job.ID
}

// Every HTTP observation used by the UI stays independent of a held lifecycle mutex.
func TestStatusReadersIgnoreLifecycleLock(t *testing.T) {
	m, profile, id := observedFixture(t)
	m.mu.Lock()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, e := m.ObserveJob(ctx, id)
		if e == nil {
			_, e = m.Status(ctx)
		}
		if e == nil {
			_, e = m.Progress(ctx, id)
		}
		if e == nil {
			_, e = m.History(ctx, profile)
		}
		done <- e
	}()
	select {
	case e := <-done:
		m.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(1500 * time.Millisecond):
		m.mu.Unlock()
		<-done
		t.Fatal("observation waited for lifecycle mutex")
	}
}

// Slow reserve I/O owns no manager lock and cannot serialize independent control reads.
func TestSlowHealthDoesNotBlockStatus(t *testing.T) {
	m, profile, id := observedFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	m.spaceProbe = func() (uint64, error) { once.Do(func() { close(entered) }); <-release; return IndexReserveBytes, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { ok, _ := m.Health(ctx); done <- ok }()
	<-entered
	readctx, stop := context.WithTimeout(context.Background(), time.Second)
	_, e := m.ObserveJob(readctx, id)
	if e == nil {
		_, e = m.Progress(readctx, id)
	}
	if e == nil {
		_, e = m.History(readctx, profile)
	}
	if e == nil {
		_, e = m.Status(readctx)
	}
	stop()
	cancel()
	close(release)
	if <-done {
		t.Error("cancelled health reported ready")
	}
	if e != nil {
		t.Fatal(e)
	}
}

// Private source authority may exceed a megabyte without enlarging public projections.
func TestStatusLargePrivateOutcomeAndDamagedSample(t *testing.T) {
	m, profile, id := observedFixture(t)
	var raw string
	if e := m.db.QueryRow(`SELECT outcome_json FROM jobs WHERE id=?`, id).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e := json.Unmarshal([]byte(raw), &v); e != nil {
		t.Fatal(e)
	}
	manifest := v["scopeManifest"].(map[string]any)
	manifest["provenance"] = map[string]any{"privatePadding": strings.Repeat("secret-private-path", 90000)}
	b, _ := json.Marshal(v)
	if _, e := m.db.Exec(`UPDATE jobs SET outcome_json=? WHERE id=?`, string(b), id); e != nil {
		t.Fatal(e)
	}
	p, e := m.Progress(context.Background(), id)
	if e != nil || p.JobID != id {
		t.Fatal(p, e)
	}
	h, e := m.History(context.Background(), profile)
	if e != nil || len(h.Jobs) != 1 {
		t.Fatal(h, e)
	}
	o, e := m.ObserveJob(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(o)
	if len(b) > 64<<10 || strings.Contains(string(b), "privatePadding") {
		t.Fatal("private outcome leaked")
	}
	if o.StoredData == nil || o.StoredData.Quality != "unverified" || o.StoredData.Freshness != "unknown" {
		t.Fatal(o.StoredData)
	}
	for _, sample := range []string{`{`, strings.Repeat("x", 9000), `{"observed":true,"at":"not-time","sample":{"knownLogicalBytes":"0"}}`} {
		if _, e = m.db.Exec(`UPDATE job_progress SET detail_json=? WHERE job_id=?`, sample, id); e != nil {
			t.Fatal(e)
		}
		p, e = m.Progress(context.Background(), id)
		if e != nil || p.Measured || p.ObservedAt != nil {
			t.Fatal(p, e)
		}
	}
	if _, e = m.db.Exec(`DELETE FROM job_progress WHERE job_id=?`, id); e != nil {
		t.Fatal(e)
	}
	p, e = m.Progress(context.Background(), id)
	if e != nil || p.Measured {
		t.Fatal(p, e)
	}
}

// Cancellation and a closed manager never revive reads or manufacture successful empty observations.
func TestStatusObservationCancellationAndClose(t *testing.T) {
	m, profile, id := observedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, read := range []func(context.Context) error{func(c context.Context) error { _, e := m.Progress(c, id); return e }, func(c context.Context) error { _, e := m.History(c, profile); return e }, func(c context.Context) error { _, e := m.ObserveJob(c, id); return e }} {
		if e := read(ctx); e == nil {
			t.Fatal("cancelled read succeeded")
		}
	}
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Progress(context.Background(), id); e == nil {
		t.Fatal("closed manager read succeeded")
	}
	if ok, _ := m.Health(context.Background()); ok {
		t.Fatal("closed manager healthy")
	}
}

// Sample writes and terminal observations share statement snapshots, never mixed sample fields.
func TestConcurrentProgressSamplesRemainCoherent(t *testing.T) {
	m, profile, id := observedFixture(t)
	var old string
	if e := m.db.QueryRow(`SELECT outcome_json FROM jobs WHERE id=?`, id).Scan(&old); e != nil {
		t.Fatal(e)
	}
	// Wait for the owned scanner to release its final presentation flush before injecting samples.
	for {
		m.mu.Lock()
		running := m.workerRunning
		m.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 80; i++ {
			sample := `{"observed":true,"at":"2026-10-10T00:00:00Z","sample":{"entries":2,"files":1,"directories":1,"knownLogicalBytes":"7"}}`
			if i%2 == 0 {
				sample = `{"observed":true,"at":"2026-10-10T00:00:01Z","sample":{"entries":4,"files":3,"directories":1,"knownLogicalBytes":"21"}}`
			}
			if _, e := m.db.Exec(`UPDATE job_progress SET detail_json=? WHERE job_id=?`, sample, id); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 40; i++ {
		h, e := m.History(context.Background(), profile)
		if e != nil {
			t.Fatal(e)
		}
		p := h.Jobs[0]
		if p.Measured && p.KnownLogicalBytes != "3" && !(p.Files == "1" && p.KnownLogicalBytes == "7") && !(p.Files == "3" && p.KnownLogicalBytes == "21") {
			t.Fatal("mixed sample", p)
		}
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

// Kernel operations are cooperative: Close may cancel health without waiting behind its syscall.
func TestCloseWhileHealthProbeIsBlocked(t *testing.T) {
	m, _, _ := observedFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	m.spaceProbe = func() (uint64, error) { close(entered); <-release; return IndexReserveBytes, nil }
	result := make(chan bool, 1)
	go func() { ok, _ := m.Health(context.Background()); result <- ok }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case e := <-closed:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(time.Second):
		close(release)
		<-result
		<-closed
		t.Fatal("Close waited for health filesystem probe")
	}
	close(release)
	if <-result {
		t.Fatal("health revived closed manager")
	}
}

// The actual joined projection must retain indexed profile traversal and avoid sorting all history.
func TestProgressProjectionHistoryPlan(t *testing.T) {
	m, _, _ := fixture(t)
	rows, e := m.db.Query(`EXPLAIN QUERY PLAN `+progressRows()+`WHERE json_extract(j.outcome_json,'$.job.profileId')=? ORDER BY j.rowid DESC LIMIT 51`, "probe")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var a, b, c int
		var d string
		if e = rows.Scan(&a, &b, &c, &d); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(d, "jobs_history_profile") {
			indexed = true
		}
		if strings.Contains(d, "TEMP B-TREE") {
			t.Fatal(d)
		}
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	if !indexed {
		t.Fatal("joined history lost profile index")
	}
}

// Wrong persisted identity and oversized relevant metadata fail rather than appearing as an empty history.
func TestProgressRejectsInvalidProjectedIdentity(t *testing.T) {
	m, profile, id := observedFixture(t)
	for _, sql := range []string{`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.id','foreign') WHERE id=?`, `UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.id',id,'$.job.startedAt',123) WHERE id=?`, `UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.startedAt',NULL,'$.job.createdAt',printf('%5000s','x')) WHERE id=?`} {
		if _, e := m.db.Exec(sql, id); e != nil {
			t.Fatal(e)
		}
		if _, e := m.Progress(context.Background(), id); e == nil {
			t.Fatal("invalid projected field accepted")
		}
		if _, e := m.History(context.Background(), profile); e == nil {
			t.Fatal("invalid history projected field accepted")
		}
	}
}

// Codes stay bounded to eight while the warning count includes all durable job warnings.
func TestProgressAllWarningCount(t *testing.T) {
	m, profile, id := observedFixture(t)
	for {
		m.mu.Lock()
		running := m.workerRunning
		m.mu.Unlock()
		if !running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	warnings := []map[string]string{}
	for i := 0; i < 12; i++ {
		warnings = append(warnings, map[string]string{"code": "SCOPE_CHANGED"})
	}
	b, _ := json.Marshal(warnings)
	if _, e := m.db.Exec(`UPDATE jobs SET outcome_json=json_set(outcome_json,'$.job.warnings',json(?)) WHERE id=?`, string(b), id); e != nil {
		t.Fatal(e)
	}
	if _, e := m.db.Exec(`UPDATE job_progress SET detail_json=? WHERE job_id=?`, `{"observed":true,"at":"2026-10-10T00:00:00Z","sample":{"entries":2,"files":1,"directories":1,"knownLogicalBytes":"3","warnings":3}}`, id); e != nil {
		t.Fatal(e)
	}
	p, e := m.Progress(context.Background(), id)
	if e != nil || p.Warnings != "15" || len(p.ErrorCodes) != 8 {
		t.Fatal(p, e)
	}
	h, e := m.History(context.Background(), profile)
	if e != nil || h.Jobs[0].Warnings != "15" || len(h.Jobs[0].ErrorCodes) != 8 {
		t.Fatal(h, e)
	}
}

// A busy control connection respects request deadlines and produces a stable unavailable error.
func TestObservationPoolDeadline(t *testing.T) {
	m, profile, id := observedFixture(t)
	m.readDB.SetMaxOpenConns(1)
	conn, e := m.readDB.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	for _, read := range []func(context.Context) error{func(c context.Context) error { _, e := m.Progress(c, id); return e }, func(c context.Context) error { _, e := m.History(c, profile); return e }, func(c context.Context) error { _, e := m.ObserveJob(c, id); return e }} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		e = read(ctx)
		cancel()
		if e != ErrUnavailable {
			t.Fatal(e)
		}
	}
}
