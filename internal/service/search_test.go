package service

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The real manager fixture distinguishes successful index reads from malformed transport rejection.
func TestSearchServiceStrictContract(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "needle.txt"), []byte("abc"), 0600)
	m, e := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	p := contracts.Profile{ID: "p", Revision: 1, Name: "Search", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if e = m.ImportProfile(jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{"share": source}, RootKinds: map[string]string{"share": "share"}, VolumeIDs: map[string]string{"share": "volume"}}); e != nil {
		t.Fatal(e)
	}
	accepted, e := m.Start("p", "full")
	if e != nil {
		t.Fatal(e)
	}
	var done jobs.Outcome
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done, e = m.Get(accepted.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if done.Job.State == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if done.Job.ResultSnapshotID == nil {
		t.Fatal(done)
	}
	view, e := m.OpenView(context.Background(), "admin", "p", "view", "", false)
	if e != nil {
		t.Fatal(e)
	}
	req := jobs.SearchRequest{ContractVersion: 1, ProfileID: "p", ViewID: view.Lease.ID, Query: query.SearchRequest{SnapshotID: *done.Job.ResultSnapshotID, RootID: "nas", Scope: "recursive", Text: "needle", Field: "both", Metric: "logical", Sort: "name", Direction: "asc", Limit: 50}}
	raw, _ := json.Marshal(req)
	mux := http.NewServeMux()
	registerSearch(mux, m)
	for _, tc := range []struct {
		name, body, principal string
		duplicate             bool
		status                int
	}{{"success", string(raw), "admin", false, 200}, {"foreign", string(raw), "other", false, 404}, {"missing", string(raw), "", false, 400}, {"duplicate", string(raw), "admin", true, 400}, {"unknown", strings.Replace(string(raw), `"profileId":"p"`, `"profileId":"p","path":"/private"`, 1), "admin", false, 400}, {"case", strings.Replace(string(raw), `"profileId"`, `"ProfileId"`, 1), "admin", false, 400}, {"oversize", strings.Repeat(" ", 32769), "admin", false, 400}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/search", strings.NewReader(tc.body))
			if tc.principal != "" {
				r.Header.Set("X-DiskMap-Principal", tc.principal)
			}
			if tc.duplicate {
				r.Header.Add("X-DiskMap-Principal", "admin")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
			if w.Code == 200 {
				var out contracts.Envelope[query.SearchPage]
				if json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Data.Items) != 1 || out.Data.Items[0].Name != "needle.txt" {
					t.Fatal(w.Body.String())
				}
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("cacheable search")
				}
			}
		})
	}
}
