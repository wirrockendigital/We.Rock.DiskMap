package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// A stopped Unix service reports a fixed diagnostic without exposing paths or starting any process.
func TestStoppedServiceDiagnostic(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "private-socket")
	var out, diagnostic bytes.Buffer
	code := run([]string{"--socket=" + socket, "scan", "--profile-id=private-profile"}, nil, &out, &diagnostic)
	if code != 4 || out.Len() != 0 || diagnostic.String() != "SERVICE_UNAVAILABLE: service unavailable for this request\n" {
		t.Fatalf("code=%d stdout=%q diagnostic=%q", code, out.String(), diagnostic.String())
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatal("stopped service socket unexpectedly created", err)
	}
}

// Oversized successful protocol bodies must not be misreported as transport unavailability.
func TestAvailabilityDiagnosticScope(t *testing.T) {
	for _, status := range []int{503, 200, 403} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			if status == 200 {
				_, _ = io.WriteString(w, strings.Repeat("x", (1<<20)+1))
			}
		}))
		client := server.Client()
		client.Transport = redirectTransport{url: server.URL, base: http.DefaultTransport}
		var diagnostic bytes.Buffer
		_, code := request(client, &diagnostic, "GET", "/api/v1/health", nil)
		server.Close()
		if status == 503 {
			if code != 4 || diagnostic.String() != "SERVICE_UNAVAILABLE: service unavailable for this request\n" {
				t.Fatal(code, diagnostic.String())
			}
		} else if diagnostic.Len() != 0 || (status == 200 && code != 4) || (status == 403 && code != 3) {
			t.Fatal(status, code, diagnostic.String())
		}
	}
}

// A truncated HTTP response exercises transport loss after response headers were already accepted.
func TestInterruptedResponseDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		_, _ = io.WriteString(w, "short")
	}))
	defer server.Close()
	client := server.Client()
	client.Transport = redirectTransport{url: server.URL, base: http.DefaultTransport}
	var diagnostic bytes.Buffer
	_, code := request(client, &diagnostic, "GET", "/api/v1/health", nil)
	if code != 4 || diagnostic.String() != "SERVICE_UNAVAILABLE: service unavailable for this request\n" {
		t.Fatal(code, diagnostic.String())
	}
}

// TestRealDaemonScan uses the actual Unix transport and scanner without a browser.
func TestRealDaemonScan(t *testing.T) {
	temp, err := os.MkdirTemp("/tmp", "dm-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(temp) })
	base, err := filepath.EvalSymlinks(temp)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "fixture")
	os.Mkdir(source, 0700)
	os.WriteFile(filepath.Join(source, "data"), []byte("real metadata"), 0600)
	socket := filepath.Join(base, "state", "run", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, socket) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	args := []string{"--socket=" + socket, "--protocol-version=1"}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var output bytes.Buffer
		if run(append(args, "health"), nil, &output, &output) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not start")
		}
		time.Sleep(time.Millisecond)
	}
	// Mapping changes use bounded stdin and the real peer-protected Unix transport.
	var mappingOutput bytes.Buffer
	if code := run(append(args, "file-type-mapping"), nil, &mappingOutput, &mappingOutput); code != 0 {
		t.Fatal(code)
	}
	var initialMapping contracts.Envelope[contracts.FileTypeMapping]
	if json.Unmarshal(mappingOutput.Bytes(), &initialMapping) != nil || initialMapping.Data.Revision != 1 {
		t.Fatal(mappingOutput.String())
	}
	mappingOutput.Reset()
	mappingPayload := []byte(`{"contractVersion":1,"expectedRevision":1,"mapping":{"version":1,"revision":2,"assignments":[{"extension":"图像","category":"image"}]}}`)
	if code := run(append(args, "file-type-mapping-set"), bytes.NewReader(mappingPayload), &mappingOutput, &mappingOutput); code != 0 {
		t.Fatal(code, mappingOutput.String())
	}
	var changedMapping contracts.Envelope[contracts.FileTypeMapping]
	if json.Unmarshal(mappingOutput.Bytes(), &changedMapping) != nil || changedMapping.Data.Revision != 2 {
		t.Fatal(mappingOutput.String())
	}
	p := contracts.Profile{ID: "probe", Revision: 1, Name: "Own fixture", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	raw, _ := json.Marshal(jobs.Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share": "share"}, Profile: p, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume-one"}})
	var output bytes.Buffer
	if code := run(append(args, "profile-import"), bytes.NewReader(raw), &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	output.Reset()
	// Exercise the real private rename transport before scanning through the unchanged ID.
	rename := []byte(`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"name":"Renamed – 東京"}`)
	if code := run(append(args, "profile-rename"), bytes.NewReader(rename), &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	var renamed contracts.Envelope[contracts.Profile]
	if json.Unmarshal(output.Bytes(), &renamed) != nil || renamed.Data.ID != "probe" || renamed.Data.Revision != 1 || renamed.Data.Name != "Renamed – 東京" {
		t.Fatal(output.String())
	}
	output.Reset()
	// Persist a configuration update over the same real Unix transport before the next scan.
	updatedProfile := renamed.Data
	updatedProfile.FreshnessSeconds = 3600
	update, _ := json.Marshal(map[string]any{"contractVersion": 1, "profileId": "probe", "expectedRevision": 1, "profile": updatedProfile})
	if code := run(append(args, "profile-update"), bytes.NewReader(update), &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	var updated contracts.Envelope[contracts.Profile]
	if json.Unmarshal(output.Bytes(), &updated) != nil || updated.Data.Revision != 2 {
		t.Fatal(output.String())
	}
	output.Reset()
	if code := run(append(args, "scan", "--profile-id=probe", "--mode=full"), nil, &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	var result contracts.Envelope[jobs.Submission]
	if json.Unmarshal(output.Bytes(), &result) != nil || result.Data.Job.State != "completed" || result.Data.Job.BytesObserved != "13" || result.Data.ServiceUID != os.Geteuid() {
		t.Fatal(output.String())
	}
	// Every supported CLI mode reaches the actual daemon and retains its requested mode.
	for _, mode := range []string{"reconcile", "rebuild"} {
		output.Reset()
		if code := run(append(args, "scan", "--profile-id=probe", "--mode="+mode), nil, &output, &output); code != 0 {
			t.Fatal(code, output.String())
		}
		if json.Unmarshal(output.Bytes(), &result) != nil || result.Data.Job.ScanMode != mode || result.Data.Job.BytesObserved != "13" {
			t.Fatal(output.String())
		}
	}
	// Preview uses the same protected Unix transport and never starts an additional job.
	output.Reset()
	previewInput := []byte(`{"contractVersion":1,"profileId":"probe","exclusions":[{"kind":"path","value":"data"}],"templateExclusions":[]}`)
	if code := run(append(args, "preview-exclusions"), bytes.NewReader(previewInput), &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	var preview contracts.Envelope[jobs.ExclusionPreview]
	if json.Unmarshal(output.Bytes(), &preview) != nil || !preview.Data.Valid || preview.Data.Matched != 1 || preview.Data.Examined != 2 {
		t.Fatal(output.String())
	}
	output.Reset()
	if code := run(append(args, "scan", "--profile-id=missing"), nil, &output, &output); code != 2 {
		t.Fatal(code)
	}
	// A genuinely deleted stable ID is never rebound or replaced by a similarly named profile.
	deletedProfile := p
	deletedProfile.ID = "deleted-profile"
	deletedProfile.Name = "Deleted fixture"
	deletedImport, _ := json.Marshal(jobs.Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share": "share"}, Profile: deletedProfile, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume-one"}})
	output.Reset()
	if code := run(append(args, "profile-import"), bytes.NewReader(deletedImport), &output, &output); code != 0 {
		t.Fatal(code, output.String())
	}
	localClient := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer localClient.CloseIdleConnections()
	revision := 1
	for _, operation := range []string{"deactivate", "delete"} {
		body := map[string]any{"contractVersion": 1, "operation": operation, "profileId": deletedProfile.ID, "expectedRevision": revision}
		if operation == "delete" {
			body["confirmOwnDataDeletion"] = true
			body["acknowledgeTasks"] = true
			body["confirmProfileName"] = deletedProfile.Name
		}
		encoded, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "http://unix/api/v1/profile-actions", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", "test-cli-admin")
		req.Header.Set("Idempotency-Key", "test-cli-"+operation)
		response, err := localClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result contracts.Envelope[jobs.ProfileActionResult]
		err = json.NewDecoder(response.Body).Decode(&result)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatal(response.StatusCode, err)
		}
		if result.Data.Profile != nil {
			revision = result.Data.Profile.Revision
		}
	}
	output.Reset()
	if code := run(append(args, "scan", "--profile-id="+deletedProfile.ID), nil, &output, &output); code != 2 {
		t.Fatal("deleted ID accepted", code, output.String())
	}
	os.Rename(source, source+"-moved")
	output.Reset()
	if code := run(append(args, "scan", "--profile-id=probe"), nil, &output, &output); code != 7 {
		t.Fatal(code, output.String())
	}
	var sink bytes.Buffer
	if code := run([]string{"--socket=" + filepath.Join(base, "missing"), "scan", "--profile-id=probe"}, nil, &sink, &sink); code != 4 {
		t.Fatal("stopped daemon", code)
	}
}

// TestTerminalExitMapping isolates protocol states without claiming a DSM integration result.
func TestTerminalExitMapping(t *testing.T) {
	for _, tc := range []struct {
		state, snapshot string
		dedup           bool
		code            int
	}{{"completed", "complete", false, 0}, {"completed", "partial", false, 7}, {"completed", "complete", true, 8}, {"completed", "partial", true, 7}, {"failed", "", false, 6}, {"interrupted", "", false, 6}, {"cancelled", "", false, 9}} {
		t.Run(fmt.Sprint(tc.state, tc.snapshot, tc.dedup), func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "dm-proto-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "s")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				job := contracts.Job{ID: "job", ProfileID: "probe", State: tc.state, ScanMode: "full", BytesObserved: "0"}
				o := jobs.Outcome{Job: job, SnapshotState: tc.snapshot}
				if tc.state == "completed" {
					snapshot := "snapshot"
					o.Job.ResultSnapshotID = &snapshot
				}
				if r.Method == "POST" {
					json.NewEncoder(w).Encode(contracts.Envelope[jobs.Submission]{ContractVersion: 1, Data: jobs.Submission{Outcome: o, Deduplicated: tc.dedup}})
				} else {
					json.NewEncoder(w).Encode(contracts.Envelope[jobs.Outcome]{ContractVersion: 1, Data: o})
				}
			})}
			go server.Serve(listener)
			defer server.Close()
			var out bytes.Buffer
			if code := run([]string{"--socket=" + socket, "scan", "--profile-id=probe"}, nil, &out, &out); code != tc.code {
				t.Fatal(code, out.String())
			}
		})
	}
}

// TestHTTPExitMapping keeps permission, input and availability failures distinguishable.
func TestHTTPExitMapping(t *testing.T) {
	for _, tc := range []struct{ status, code int }{{400, 2}, {403, 3}, {409, 5}, {500, 6}, {503, 4}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) }))
		client := server.Client()
		client.Transport = redirectTransport{url: server.URL, base: http.DefaultTransport}
		_, code := request(client, io.Discard, "GET", "/api/v1/health", nil)
		server.Close()
		if code != tc.code {
			t.Fatalf("status %d: got %d want %d", tc.status, code, tc.code)
		}
	}
}

// redirectTransport routes the fixed local protocol host to an isolated HTTP test server.
type redirectTransport struct {
	url  string
	base http.RoundTripper
}

func (t redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u, err := url.Parse(t.url)
	if err != nil {
		return nil, err
	}
	r.URL.Scheme = u.Scheme
	r.URL.Host = u.Host
	return t.base.RoundTrip(r)
}

// TestUnixPermissionExit exercises a real kernel-denied socket directory without privilege changes.
func TestUnixPermissionExit(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission denial requires an unprivileged test user")
	}
	dir, err := os.MkdirTemp("/tmp", "dm-denied-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Chmod(dir, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	var output bytes.Buffer
	if code := run([]string{"--socket=" + socket, "health"}, nil, &output, &output); code != 3 {
		t.Fatalf("permission denied mapped to %d instead of 3", code)
	}
}

// TestInventoryCommandUsesDaemonSocket verifies the CLI only forwards the local inventory query.
func TestInventoryCommandUsesDaemonSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "dm-inventory-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/inventory" {
			t.Error(r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"contractVersion":1,"requestId":"inventory","data":{"volumes":[],"issues":[]}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	var out bytes.Buffer
	if code := run([]string{"--socket=" + socket, "inventory"}, nil, &out, &out); code != 0 || !bytes.Contains(out.Bytes(), []byte(`"volumes"`)) {
		t.Fatal(code, out.String())
	}
	if code := run([]string{"--socket=" + socket, "inventory", "extra"}, nil, &out, &out); code != 2 {
		t.Fatal(code)
	}
}
