package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// The unchanged job route reports publication separately from unverified present-day index availability.
func TestJobObservationRecordedOutcome(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(base, "source")
	if e = os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	m, e := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	p := contracts.Profile{ID: "probe", Revision: 1, Name: "Probe", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if e = m.ImportProfile(jobs.Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share": "share"}, Profile: p, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume"}}); e != nil {
		t.Fatal(e)
	}
	sub, e := m.Start(p.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	var out jobs.Outcome
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		out, e = m.WaitStatus(context.Background(), sub.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if out.Job.State == "completed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if out.Job.State != "completed" {
		t.Fatal(out.Job.State)
	}
	mux := http.NewServeMux()
	registerJobs(mux, m)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/jobs/"+sub.Job.ID, nil))
	var got contracts.Envelope[jobs.Outcome]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if got.Data.Job.ID != sub.Job.ID || got.Data.Job.ProfileID != p.ID || got.Data.Job.ScanMode != "full" || got.Data.Job.ResultSnapshotID == nil || got.Data.SnapshotState != "complete" || len(got.Data.PhaseHistory) != 6 {
		t.Fatal(got.Data)
	}
	if got.Data.StoredData == nil || got.Data.StoredData.Quality != "unverified" || got.Data.StoredData.Freshness != "unknown" || got.Data.StoredData.AgeSeconds != nil {
		t.Fatal(got.Data.StoredData)
	}
	// An aborted public read has a controlled unavailable response, not an internal-error/HTML fallback.
	canceled, stop := context.WithCancel(context.Background())
	stop()
	rejected := httptest.NewRecorder()
	mux.ServeHTTP(rejected, httptest.NewRequest("GET", "/api/v1/jobs/"+sub.Job.ID, nil).WithContext(canceled))
	if rejected.Code != 503 {
		t.Fatal(rejected.Code, rejected.Body.String())
	}
	// Real generation readers retain their original qualification rather than inheriting this observation.
	actual, e := m.Get(sub.Job.ID)
	if e != nil || actual.StoredData.Quality != "complete" {
		t.Fatal(actual, e)
	}
}
