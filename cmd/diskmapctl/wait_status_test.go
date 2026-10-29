package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Transient polls retry the accepted job only; successful phases reset the consecutive budget.
func TestCLIWaitStatusTransientBudget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		states     []string
		exit       int
		diagnostic bool
	}{{"recover", []string{"503", "503", "paused", "503", "scanning", "completed"}, 0, false}, {"exhaust", []string{"503", "503", "503"}, 4, true}, {"invalid-protocol", []string{"malformed"}, 4, false}, {"denied", []string{"denied"}, 3, false}} {
		t.Run(tc.name, func(t *testing.T) {
			posts, polls := 0, 0
			socket := cliFixtureServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				o := jobs.Outcome{Job: contracts.Job{BytesObserved: "0", ID: "accepted-job", ProfileID: "profile", ScanMode: "full", State: "queued"}}
				if r.Method == "POST" {
					posts++
					json.NewEncoder(w).Encode(contracts.Envelope[jobs.Submission]{ContractVersion: 1, Data: jobs.Submission{Outcome: o}})
					return
				}
				if r.URL.Path != "/api/v1/jobs/accepted-job/wait-status" {
					t.Error("incorrect poll binding", r.URL.Path)
					w.WriteHeader(400)
					return
				}
				if polls >= len(tc.states) {
					t.Error("excess retry")
					w.WriteHeader(400)
					return
				}
				state := tc.states[polls]
				polls++
				switch state {
				case "503":
					w.WriteHeader(503)
					return
				case "denied":
					w.WriteHeader(403)
					return
				case "malformed":
					w.Write([]byte("invalid"))
					return
				}
				o.Job.State = state
				if state == "completed" {
					snapshot := "snapshot"
					o.Job.ResultSnapshotID = &snapshot
					o.SnapshotState = "complete"
				}
				json.NewEncoder(w).Encode(contracts.Envelope[jobs.Outcome]{ContractVersion: 1, Data: o})
			}))
			var out, diag bytes.Buffer
			code := run([]string{"--socket=" + socket, "scan", "--profile-id=profile"}, nil, &out, &diag)
			if code != tc.exit || posts != 1 || polls != len(tc.states) || (diag.Len() > 0) != tc.diagnostic || strings.Count(diag.String(), "SERVICE_UNAVAILABLE") > 1 {
				t.Fatal(code, posts, polls, diag.String())
			}
		})
	}
}
