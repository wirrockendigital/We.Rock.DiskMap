package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Contract discovery is static and rejects unsupported versions without opening a socket.
func TestExitContractVersionAndStableIDs(t *testing.T) {
	var out, diag bytes.Buffer
	if code := run([]string{"--exit-contract-version=1", "exit-contract"}, nil, &out, &diag); code != 0 {
		t.Fatal(code)
	}
	var contract struct {
		Version int            `json:"exitContractVersion"`
		Codes   map[string]int `json:"codes"`
	}
	if json.Unmarshal(out.Bytes(), &contract) != nil || contract.Version != 1 || contract.Codes["partial"] != 7 || contract.Codes["deduplicatedComplete"] != 8 {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"--exit-contract-version=2", "exit-contract"}, {"--protocol-version=2", "exit-contract"}, {"--socket=/absent", "scan", "--profile-id=../path"}, {"--socket=/absent", "scan", "--profile-id=Profile Name"}, {"--socket=/absent", "scan", "--profile-id="}} {
		out.Reset()
		diag.Reset()
		if code := run(args, nil, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatal(args, code, out.String())
		}
	}
}

// A local fake transport qualifies only CLI waiting/protocol handling, never native authorization.
func cliFixtureServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dm-cli-contract-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close(); os.RemoveAll(dir) })
	return socket
}

// Joining never exits early: all queued, active and paused phases are observed before terminal output.
func TestCLIWaitsAcrossEveryNonterminalPhase(t *testing.T) {
	phases := []string{"queued", "preflight", "scanning", "paused", "aggregating", "validating", "publishing", "completed"}
	entered := make(chan string)
	release := make(chan struct{})
	defer close(release)
	polls := 0
	socket := cliFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := jobs.Outcome{Job: contracts.Job{ID: "job", ProfileID: "profile", ScanMode: "full", State: "queued", BytesObserved: "0"}}
		if r.Method == "POST" {
			json.NewEncoder(w).Encode(contracts.Envelope[jobs.Submission]{ContractVersion: 1, Data: jobs.Submission{Outcome: o, Deduplicated: true}})
			return
		}
		if polls >= len(phases) {
			t.Error("unexpected poll")
			w.WriteHeader(500)
			return
		}
		state := phases[polls]
		polls++
		entered <- state
		<-release
		o.Job.State = state
		if state == "completed" {
			snapshot := "snapshot"
			o.Job.ResultSnapshotID = &snapshot
			o.SnapshotState = "complete"
		}
		json.NewEncoder(w).Encode(contracts.Envelope[jobs.Outcome]{ContractVersion: 1, Data: o})
	}))
	var out, diag bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"--socket=" + socket, "scan", "--profile-id=profile"}, nil, &out, &diag) }()
	for _, phase := range phases {
		select {
		case got := <-entered:
			if got != phase {
				t.Fatal(got, phase)
			}
		case code := <-done:
			t.Fatal("returned before terminal", code)
		case <-time.After(3 * time.Second):
			t.Fatal("poll stalled")
		}
		select {
		case code := <-done:
			t.Fatal("returned during phase", phase, code)
		default:
		}
		release <- struct{}{}
	}
	select {
	case code := <-done:
		if code != 8 {
			t.Fatal(code, diag.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("terminal did not return")
	}
	var result cliResult
	if json.Unmarshal(out.Bytes(), &result) != nil || result.ExitContractVersion != 1 || !result.Data.Deduplicated || result.Data.Job.State != "completed" {
		t.Fatal(out.String())
	}
}

// Invalid accepted identity cannot redirect subsequent polling to another route or profile.
func TestCLIBindsAcceptanceAndRejectsInvalidTerminal(t *testing.T) {
	for _, bad := range []string{"job-id", "profile", "mode", "state", "missing-snapshot"} {
		t.Run(bad, func(t *testing.T) {
			gets := 0
			socket := cliFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				o := jobs.Outcome{Job: contracts.Job{ID: "job", ProfileID: "profile", ScanMode: "full", State: "failed", BytesObserved: "0"}}
				if r.Method == "POST" {
					if bad == "job-id" {
						o.Job.ID = "../health"
					}
					if bad == "profile" {
						o.Job.ProfileID = "other"
					}
					if bad == "mode" {
						o.Job.ScanMode = "rebuild"
					}
					json.NewEncoder(w).Encode(contracts.Envelope[jobs.Submission]{ContractVersion: 1, Data: jobs.Submission{Outcome: o}})
					return
				}
				gets++
				if bad == "state" {
					o.Job.State = "unknown"
				}
				if bad == "missing-snapshot" {
					o.Job.State = "completed"
					o.SnapshotState = "complete"
				}
				json.NewEncoder(w).Encode(contracts.Envelope[jobs.Outcome]{ContractVersion: 1, Data: o})
			}))
			var out, diag bytes.Buffer
			if code := run([]string{"--socket=" + socket, "scan", "--profile-id=profile"}, nil, &out, &diag); code != 4 || out.Len() != 0 {
				t.Fatal(code, out.String())
			}
			if (bad == "job-id" || bad == "profile" || bad == "mode") && gets != 0 {
				t.Fatal("untrusted acceptance polled", gets)
			}
		})
	}
}

// Dedupe is provenance, never precedence over a terminal failure or incomplete coverage.
func TestTerminalPrecedence(t *testing.T) {
	snapshot := "snapshot"
	for _, tc := range []struct {
		state, quality string
		code           int
	}{{"completed", "complete", 8}, {"completed", "partial", 7}, {"failed", "", 6}, {"interrupted", "", 6}, {"cancelled", "", 9}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			o := jobs.Outcome{Job: contracts.Job{State: tc.state}, SnapshotState: tc.quality}
			if tc.state == "completed" {
				o.Job.ResultSnapshotID = &snapshot
			}
			code, waiting := terminalExit(o, true)
			if code != tc.code || waiting {
				t.Fatal(code, waiting)
			}
		})
	}
}

// Parser failures never reflect private flag names or supplied values into diagnostics.
func TestCLIParserDiagnosticsRedacted(t *testing.T) {
	for _, args := range [][]string{{"--protocol-version=/private/name", "health"}, {"--exit-contract-version=private-secret", "health"}, {"--private-secret=value"}, {"--socket=/not-contacted", "scan", "--private-secret=value"}} {
		var out, diag bytes.Buffer
		if code := run(args, nil, &out, &diag); code != 2 || out.Len() != 0 || diag.String() != "INVALID_ARGUMENTS: invalid command arguments\n" {
			t.Fatal(code, out.String(), diag.String())
		}
	}
}
