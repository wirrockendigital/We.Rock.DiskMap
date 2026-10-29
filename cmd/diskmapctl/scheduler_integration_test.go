package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// schedulerRig exercises the real service and CLI; the proxy only observes acceptance receipts.
type schedulerRig struct {
	t                         *testing.T
	base, socket, proxySocket string
	client                    *http.Client
	accepted                  chan jobs.Submission
}

func newSchedulerRig(t *testing.T) *schedulerRig {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "dm-sched-")
	if err != nil {
		t.Fatal(err)
	}
	base, err = filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	r := &schedulerRig{t: t, base: base, socket: filepath.Join(base, "state/run/control.sock"), proxySocket: filepath.Join(base, "observe.sock"), accepted: make(chan jobs.Submission, 16)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, r.socket) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", r.socket)
	}}
	r.client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("service shutdown timeout")
		}
		transport.CloseIdleConnections()
		os.RemoveAll(base)
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		response, e := r.client.Get("http://unix/api/v1/health")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("service startup timeout")
		}
		time.Sleep(time.Millisecond)
	}
	target, _ := url.Parse("http://unix")
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.Method == "POST" && response.Request.URL.Path == "/api/v1/jobs" && response.StatusCode == 200 {
			raw, e := io.ReadAll(response.Body)
			response.Body.Close()
			if e != nil {
				return e
			}
			response.Body = io.NopCloser(bytes.NewReader(raw))
			var envelope contracts.Envelope[jobs.Submission]
			if e = json.Unmarshal(raw, &envelope); e != nil {
				return e
			}
			r.accepted <- envelope.Data
		}
		return nil
	}
	listener, e := net.Listen("unix", r.proxySocket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: proxy}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return r
}

// call preserves the same trusted CGI-to-service identity pair without claiming DSM authentication.
func (r *schedulerRig) call(method, path string, payload any, key string, out any) {
	r.t.Helper()
	raw, _ := json.Marshal(payload)
	req, e := http.NewRequest(method, "http://unix"+path, bytes.NewReader(raw))
	if e != nil {
		r.t.Fatal(e)
	}
	if key != "" {
		req.Header.Set("X-DiskMap-Principal", "scheduler-test-admin")
		req.Header.Set("Idempotency-Key", key)
	}
	response, e := r.client.Do(req)
	if e != nil {
		r.t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		r.t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, body)
	}
	if out != nil {
		if e = json.NewDecoder(response.Body).Decode(out); e != nil {
			r.t.Fatal(e)
		}
	}
}

// importFixture creates only local metadata fixtures and an explicit, isolated scope.
func (r *schedulerRig) importFixture(id string, count int) (contracts.Profile, string) {
	r.t.Helper()
	source := filepath.Join(r.base, id)
	if e := os.Mkdir(source, 0700); e != nil {
		r.t.Fatal(e)
	}
	for n := 0; n < count; n++ {
		if e := os.WriteFile(filepath.Join(source, fmt.Sprint(n)), []byte("x"), 0600); e != nil {
			r.t.Fatal(e)
		}
	}
	p := contracts.Profile{ID: id, Revision: 1, Name: id, Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{id + "-root"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	r.call("POST", "/api/v1/profiles/import", jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{id + "-root": source}, RootKinds: map[string]string{id + "-root": "share"}, VolumeIDs: map[string]string{id + "-root": "volume"}}, "", nil)
	return p, source
}

func (r *schedulerRig) control(id, action string) {
	r.t.Helper()
	r.call("POST", "/api/v1/job-control", map[string]any{"contractVersion": 1, "jobId": id, "action": action}, id+"-"+action, nil)
}
func (r *schedulerRig) get(id string) jobs.Outcome {
	r.t.Helper()
	var out contracts.Envelope[jobs.Outcome]
	r.call("GET", "/api/v1/jobs/"+id, nil, "", &out)
	return out.Data
}
func (r *schedulerRig) wait(id, state string) jobs.Outcome {
	r.t.Helper()
	until := time.Now().Add(20 * time.Second)
	for {
		out := r.get(id)
		if out.Job.State == state {
			return out
		}
		if time.Now().After(until) {
			r.t.Fatalf("expected %s, got %s", state, out.Job.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type schedulerCLIResult struct {
	code       int
	out        jobs.Submission
	diagnostic string
}

func (r *schedulerRig) cli(id string) <-chan schedulerCLIResult {
	result := make(chan schedulerCLIResult, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := run([]string{"--socket=" + r.proxySocket, "scan", "--profile-id=" + id}, nil, &stdout, &stderr)
		var envelope contracts.Envelope[jobs.Submission]
		_ = json.Unmarshal(stdout.Bytes(), &envelope)
		result <- schedulerCLIResult{code, envelope.Data, stderr.String()}
	}()
	return result
}
func (r *schedulerRig) receipt() jobs.Submission {
	r.t.Helper()
	select {
	case out := <-r.accepted:
		return out
	case <-time.After(10 * time.Second):
		r.t.Fatal("CLI acceptance timeout")
	}
	return jobs.Submission{}
}
func schedulerResult(t *testing.T, ch <-chan schedulerCLIResult) schedulerCLIResult {
	t.Helper()
	select {
	case out := <-ch:
		return out
	// This is an end-to-end FIFO assertion, not a filesystem throughput benchmark.
	case <-time.After(60 * time.Second):
		t.Fatal("CLI terminal timeout")
	}
	return schedulerCLIResult{}
}

// UI acceptance plus two real CLI waiters must remain one job, preserving the first trigger and capture.
func TestSchedulerUIAndCLIShareFIFO(t *testing.T) {
	for _, action := range []string{"resume", "cancel"} {
		t.Run(action, func(t *testing.T) {
			r := newSchedulerRig(t)
			_, source := r.importFixture("first", 4096)
			second, _ := r.importFixture("second", 1)
			var ui contracts.Envelope[jobs.Submission]
			r.call("POST", "/api/v1/jobs", map[string]any{"contractVersion": 1, "profileId": "first", "mode": "full"}, "ui-first", &ui)
			r.control(ui.Data.Job.ID, "pause")
			r.wait(ui.Data.Job.ID, "paused")
			one, two := r.cli("first"), r.cli("first")
			for n := 0; n < 2; n++ {
				joined := r.receipt()
				if !joined.Deduplicated || joined.Job.ID != ui.Data.Job.ID || joined.Job.Trigger != "ui" || joined.Job.ProfileRevision != 1 {
					t.Fatal("joined intent changed", joined)
				}
			}
			other := r.cli("second")
			queued := r.receipt()
			if queued.Deduplicated || queued.Job.State != "queued" || queued.Job.ID == ui.Data.Job.ID {
				t.Fatal("second profile did not queue", queued)
			}
			// A later profile edit does not rewrite the already accepted queue capture.
			second.FreshnessSeconds = 3600
			r.call("POST", "/api/v1/profiles/update", map[string]any{"contractVersion": 1, "profileId": "second", "expectedRevision": 1, "profile": second}, "", nil)
			r.control(ui.Data.Job.ID, action)
			expected := 8
			terminal := "completed"
			if action == "cancel" {
				expected = 9
				terminal = "cancelled"
			}
			for _, ch := range []<-chan schedulerCLIResult{one, two} {
				out := schedulerResult(t, ch)
				if out.code != expected || out.out.Job.ID != ui.Data.Job.ID || !out.out.Deduplicated || out.out.Job.State != terminal || out.out.ServiceUID != os.Geteuid() {
					t.Fatalf("joined terminal: %+v", out)
				}
			}
			next := schedulerResult(t, other)
			if next.code != 0 || next.out.Job.ProfileRevision != 1 || next.out.Job.BytesObserved != "1" || next.out.ServiceUID != os.Geteuid() {
				t.Fatalf("queued terminal: %+v", next)
			}
			firstFinal := r.get(ui.Data.Job.ID)
			if firstFinal.Job.EndedAt == nil || next.out.Job.StartedAt == nil || *next.out.Job.StartedAt < *firstFinal.Job.EndedAt {
				t.Fatal("FIFO timestamps overlap")
			}
			if next.out.ScopeManifest == nil || len(next.out.ScopeManifest.Roots) != 1 || next.out.ScopeManifest.Roots[0].TargetID != "second-root" {
				t.Fatal("queued scope changed")
			}
			// Missing sources produce a real persisted partial result and CLI exit 7, never a complete scan.
			if e := os.Rename(source, source+"-away"); e != nil {
				t.Fatal(e)
			}
			partial := r.cli("first")
			r.receipt()
			got := schedulerResult(t, partial)
			if got.code != 7 || got.out.SnapshotState != "partial" || got.out.CoverageErrors == 0 {
				t.Fatalf("partial terminal: %+v", got)
			}
		})
	}
}

// Persisted deactivation and deletion never redirect a stale task to another profile or NAS scope.
func TestSchedulerStaleIDsFailClosed(t *testing.T) {
	r := newSchedulerRig(t)
	p, _ := r.importFixture("old-profile", 1)
	for _, id := range []string{"missing-profile", "../private-root", ""} {
		got := schedulerResult(t, r.cli(id))
		if got.code != 2 {
			t.Fatalf("invalid identity accepted: code %d", got.code)
		}
		if id == "missing-profile" && got.diagnostic != "SCAN_PROFILE_REJECTED: profile is missing, inactive or not valid for scanning; check it in the app and copy the current task template\n" {
			t.Fatal("missing profile diagnosis", got.diagnostic)
		}
	}
	revision := 1
	for _, operation := range []string{"deactivate", "delete"} {
		body := map[string]any{"contractVersion": 1, "operation": operation, "profileId": p.ID, "expectedRevision": revision}
		if operation == "delete" {
			body["confirmOwnDataDeletion"] = true
			body["acknowledgeTasks"] = true
			body["confirmProfileName"] = p.Name
		}
		var result contracts.Envelope[jobs.ProfileActionResult]
		r.call("POST", "/api/v1/profile-actions", body, "stale-"+operation, &result)
		if result.Data.Profile != nil {
			revision = result.Data.Profile.Revision
		}
		got := schedulerResult(t, r.cli(p.ID))
		if got.code != 2 || got.diagnostic != "SCAN_PROFILE_REJECTED: profile is missing, inactive or not valid for scanning; check it in the app and copy the current task template\n" {
			t.Fatalf("%s profile diagnosis: %+v", operation, got)
		}
	}
	replacement, _ := r.importFixture("new-profile", 1)
	r.call("POST", "/api/v1/profiles/rename", map[string]any{"contractVersion": 1, "profileId": replacement.ID, "expectedRevision": 1, "name": p.Name}, "", nil)
	if got := schedulerResult(t, r.cli(p.ID)); got.code != 2 {
		t.Fatal("old ID rebound by display name", got)
	}
	// The fresh explicit ID remains usable; this is a new profile rather than an implicit alias.
	fresh := r.cli(replacement.ID)
	r.receipt()
	if got := schedulerResult(t, fresh); got.code != 0 || got.out.Job.ProfileID != replacement.ID {
		t.Fatal("replacement profile", got)
	}
}
