package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// TestPublicModesReachRealService verifies CGI, peer-guarded socket, manager and scanner together.
func TestPublicModesReachRealService(t *testing.T) {
	base, e := os.MkdirTemp("/tmp", "dm-modes-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(base)
	base, e = filepath.EvalSymlinks(base)
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(base, "source")
	if e = os.Mkdir(source, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(source, "file"), []byte("real"), 0600); e != nil {
		t.Fatal(e)
	}
	socket := filepath.Join(base, "state", "run", "control.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx, socket) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not stop")
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, e := client.Get("http://unix/api/v1/health")
		if e == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	p := contracts.Profile{ID: "probe", Revision: 1, Name: "Probe", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	payload, _ := json.Marshal(jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{"share": source}, RootKinds: map[string]string{"share": "share"}, VolumeIDs: map[string]string{"share": "volume"}})
	response, e := client.Post("http://unix/api/v1/profiles/import", "application/json", bytes.NewReader(payload))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(raw))
	}
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: client}
	send := func(mode, key string) *httptest.ResponseRecorder {
		r := scanRequest(`{"contractVersion":1,"profileId":"probe","mode":"` + mode + `"}`)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	for _, mode := range []string{"full", "rebuild", "reconcile"} {
		key := "mode-" + mode
		w := send(mode, key)
		var accepted contracts.Envelope[jobs.Submission]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &accepted) != nil || accepted.Data.Job.ScanMode != mode {
			t.Fatal(w.Code, w.Body.String())
		}
		deadline = time.Now().Add(5 * time.Second)
		for {
			r, e := client.Get("http://unix/api/v1/jobs/" + accepted.Data.Job.ID)
			if e != nil {
				t.Fatal(e)
			}
			var out contracts.Envelope[jobs.Outcome]
			e = json.NewDecoder(r.Body).Decode(&out)
			r.Body.Close()
			if e != nil {
				t.Fatal(e)
			}
			if out.Data.Job.State == "completed" {
				if out.Data.SnapshotState != "complete" || out.Data.Job.BytesObserved != "4" || out.Data.Job.ScanMode != mode {
					t.Fatal(out)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal(out)
			}
			time.Sleep(time.Millisecond)
		}
		replay := send(mode, key)
		var again contracts.Envelope[jobs.Submission]
		if replay.Code != 200 || json.Unmarshal(replay.Body.Bytes(), &again) != nil || again.Data.Job.ID != accepted.Data.Job.ID || !again.Data.Deduplicated {
			t.Fatal(replay.Code, replay.Body.String())
		}
		other := "full"
		if mode == other {
			other = "rebuild"
		}
		conflict := send(other, key)
		if conflict.Code != 409 || !strings.Contains(conflict.Body.String(), "IDEMPOTENCY_CONFLICT") {
			t.Fatal(conflict.Code, conflict.Body.String())
		}
	}
	for _, mode := range []string{"refresh", "incremental", "FULL"} {
		if w := send(mode, "invalid-"+mode); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
