package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

// Progress and history must preserve the final session check before disclosing observations.
func TestProgressReadGuards(t *testing.T) {
	for _, action := range []string{"progress&jobId=job-1", "history&profileId=profile-1"} {
		valid, calls := true, 0
		b := bridge{identity: func(context.Context) (string, error) {
			if !valid {
				return "", dsmauth.ErrRole
			}
			return "test-admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
				t.Fatal(r.URL.Path)
			}
			valid = false
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"private":"progress-observation"}`))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action="+action))
		if w.Code != 403 || calls != 1 || strings.Contains(w.Body.String(), "progress-observation") {
			t.Fatal(w.Code, calls)
		}
		valid = true
		w = httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action="+action+"&path=/etc"))
		if w.Code != 400 || calls != 1 {
			t.Fatal("unknown selector reached backend", w.Code, calls)
		}
	}
}

// A real service and CGI preserve a near-limit captured public receipt without its private authority blob.
func TestLargePublicJobObservationThroughService(t *testing.T) {
	base, e := os.MkdirTemp("/tmp", "dm-observe-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	state := filepath.Join(base, "state")
	m, e := jobs.Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	o := jobs.Outcome{Job: contracts.Job{ID: "large-observation", ProfileID: "profile", ProfileRevision: 1, State: "failed", BytesObserved: contracts.Bytes("0"), EntriesVisited: "0", ScanMode: "full", Trigger: "cli", CreatedAt: "2026-10-10T00:00:00Z"}, ScopeManifest: &scopemanifest.ScopeManifest{ContractVersion: 2, Kind: "nas", Roots: []scopemanifest.ManifestRoot{}, CoveredTargetIDs: []string{}, Issues: []scopemanifest.ScopeIssue{}}}
	o.Job.CapturedProfile = contracts.Profile{ID: "profile", Revision: 1, Name: "Profile", Scope: contracts.Scope{Kind: "nas", TargetIDs: []string{}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	// Disjoint captured unavailable roots are metadata, not new access to these paths.
	for i := 0; i < 128; i++ {
		id := fmt.Sprintf("share-%03d", i)
		o.ScopeManifest.Roots = append(o.ScopeManifest.Roots, scopemanifest.ManifestRoot{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: id, ShareID: &id, Kind: "share", VolumeID: "volume", PhysicalPath: "/volume1/" + strings.Repeat("n", 120) + "/" + strings.Repeat("n", 179) + id, State: "unreadable"}})
		o.ScopeManifest.CoveredTargetIDs = append(o.ScopeManifest.CoveredTargetIDs, id)
	}
	raw, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) < 55*1024 || len(raw) > 63*1024 {
		t.Fatal("fixture budget", len(raw))
	}
	db, e := index.Open(filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO jobs(id,state,outcome_json)VALUES(?,?,?)`, o.Job.ID, o.Job.State, string(raw)); e != nil {
		t.Fatal(e)
	}
	db.Close()
	socket := filepath.Join(state, "run", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, socket) }()
	defer func() {
		cancel()
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(c, "unix", socket)
	}}}
	defer client.CloseIdleConnections()
	for end := time.Now().Add(5 * time.Second); ; {
		r, e := client.Get("http://unix/api/v1/health")
		if e == nil {
			r.Body.Close()
			break
		}
		if time.Now().After(end) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	b := bridge{identity: func(context.Context) (string, error) { return "admin", nil }, client: client}
	w := httptest.NewRecorder()
	b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=job&id="+o.Job.ID))
	var got contracts.Envelope[jobs.Outcome]
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Data.Job.ID != o.Job.ID || len(got.Data.ScopeManifest.Roots) != 128 || w.Body.Len() > 64*1024 {
		t.Fatal(w.Code, w.Body.Len(), w.Body.String())
	}
}
