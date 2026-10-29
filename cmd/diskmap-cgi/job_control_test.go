package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGuardedJobControlUsesRealManager drives CGI, service, durable replay and actual worker state together.
func TestGuardedJobControlUsesRealManager(t *testing.T) {
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(base, "source")
	if e = os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2000; i++ {
		if e = os.WriteFile(filepath.Join(source, fmt.Sprint(i)), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m, e := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	p := contracts.Profile{ID: "control", Revision: 1, Name: "Control", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if e = m.ImportProfile(jobs.Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share": "share"}, Profile: p, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume"}}); e != nil {
		t.Fatal(e)
	}
	sub, e := m.Start(p.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	handler := service.JobControlHandler(m)
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/v1/job-control" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Error("unsafe forwarding")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}}
	send := func(action, key string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"contractVersion":1,"jobId":%q,"action":%q}`, sub.Job.ID, action)
		r := scanRequest(body)
		r.URL.RawQuery = "action=job-control"
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	var accepted *httptest.ResponseRecorder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		accepted = send("pause", "pause")
		if accepted.Code == 200 {
			break
		}
		if accepted.Code != 409 {
			t.Fatal(accepted.Code, accepted.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
	if accepted.Code != 200 {
		t.Fatal("pause was not accepted")
	}
	for time.Now().Before(deadline) {
		o, err := m.Get(sub.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if o.Job.State == "paused" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	o, e := m.Get(sub.Job.ID)
	if e != nil || o.Job.State != "paused" {
		t.Fatal(o, e)
	}
	if replay := send("pause", "pause"); replay.Code != 200 || replay.Body.String() != accepted.Body.String() {
		t.Fatal("replay changed", replay.Body.String())
	}
	var wg sync.WaitGroup
	responses := make([]*httptest.ResponseRecorder, 8)
	for i := range responses {
		wg.Add(1)
		go func(i int) { defer wg.Done(); responses[i] = send("resume", "resume") }(i)
	}
	wg.Wait()
	for _, r := range responses {
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if replay := send("pause", "pause"); replay.Code != 200 {
		t.Fatal(replay.Code)
	}
	cancelled := send("cancel", "cancel")
	if cancelled.Code != 200 {
		t.Fatal(cancelled.Code, cancelled.Body.String())
	}
	for time.Now().Before(deadline) {
		o, e = m.Get(sub.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if o.Job.State == "cancelled" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if o.Job.State != "cancelled" {
		t.Fatal(o)
	}
	var receipt map[string]any
	if json.Unmarshal(cancelled.Body.Bytes(), &receipt) != nil {
		t.Fatal("invalid receipt")
	}
}

// TestJobControlRevocationAndExactSchema denies real side effects before any service call.
func TestJobControlRevocationAndExactSchema(t *testing.T) {
	for _, failure := range []string{"session", "role", "origin", "case"} {
		t.Run(failure, func(t *testing.T) {
			valid := true
			calls := 0
			b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
				if !valid {
					if failure == "role" {
						return "", dsmauth.ErrRole
					}
					return "", dsmauth.ErrSession
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected") })}}
			body := `{"contractVersion":1,"jobId":"job","action":"cancel"}`
			if failure == "case" {
				body = strings.Replace(body, "jobId", "JobId", 1)
			}
			r := scanRequest(body)
			r.URL.RawQuery = "action=job-control"
			expected := 400
			if failure == "session" || failure == "role" {
				r.Body = io.NopCloser(changingReader{r.Body, func() { valid = false }})
				expected = 401
				if failure == "role" {
					expected = 403
				}
			}
			if failure == "origin" {
				r.Header.Set("Origin", "https://foreign.test")
				expected = 403
			}
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if w.Code != expected || calls != 0 {
				t.Fatal(w.Code, calls)
			}
		})
	}
}
