package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// TestBrowserJobHeaderBoundary exercises the private handler behind peer authorization.
func TestBrowserJobHeaderBoundary(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "fixture")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	profile := contracts.Profile{ID: "probe", Revision: 1, Name: "Probe", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if err = manager.ImportProfile(jobs.Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share": "share"}, Profile: profile, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume"}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerJobs(mux, manager)
	payload := `{"contractVersion":1,"profileId":"probe","mode":"full"}`
	send := func(body string, headers http.Header) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "/api/v1/jobs", strings.NewReader(body))
		request.Header = headers
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
	for _, headers := range []http.Header{
		{"Idempotency-Key": {"key"}},
		{"X-Diskmap-Principal": {"admin"}},
		{"Idempotency-Key": {""}, "X-Diskmap-Principal": {"admin"}},
		{"Idempotency-Key": {"key", "key"}, "X-Diskmap-Principal": {"admin"}},
		{"Idempotency-Key": {"key"}, "X-Diskmap-Principal": {"admin", "other"}},
		{"Idempotency-Key": {"bad key"}, "X-Diskmap-Principal": {"admin"}},
	} {
		if response := send(payload, headers); response.Code != 400 {
			t.Fatal("malformed headers accepted", response.Code, response.Body.String())
		}
	}
	headers := http.Header{"Idempotency-Key": {"valid-key"}, "X-Diskmap-Principal": {"prüfer"}}
	first := send(payload, headers)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	var accepted contracts.Envelope[jobs.Submission]
	if err = json.Unmarshal(first.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	replay := send(`{ "mode":"full", "profileId":"probe", "contractVersion":1 }`, headers)
	var repeated contracts.Envelope[jobs.Submission]
	if replay.Code != 200 || json.Unmarshal(replay.Body.Bytes(), &repeated) != nil || repeated.Data.Job.ID != accepted.Data.Job.ID || !repeated.Data.Deduplicated {
		t.Fatal(replay.Code, replay.Body.String())
	}
	conflict := send(`{"contractVersion":1,"profileId":"another","mode":"full"}`, headers)
	if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(conflict.Code, conflict.Body.String())
	}
	// The CLI continues to use the existing keyless socket protocol.
	if plain := send(payload, http.Header{}); plain.Code != 200 {
		t.Fatal(plain.Code, plain.Body.String())
	}
}

// TestQueueFullErrorPreservesReason exposes capacity distinctly without changing the conflict status.
func TestQueueFullErrorPreservesReason(t *testing.T) {
	response := httptest.NewRecorder()
	jobError(response, jobs.ErrQueueFull)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `"code":"QUEUE_FULL"`) {
		t.Fatal(response.Code, response.Body.String())
	}
}
