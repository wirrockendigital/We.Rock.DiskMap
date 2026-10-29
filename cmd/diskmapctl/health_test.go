package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestHealthExitUsesReadiness exercises the same Unix transport used by DSM lifecycle hooks.
func TestHealthExitUsesReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"ready", `{"contractVersion":1,"requestId":"health","data":{"status":"ready","ready":true,"reasonCode":""}}`, 0},
		{"reserve exhausted", `{"contractVersion":1,"requestId":"health","data":{"status":"unavailable","ready":false,"reasonCode":"STATE_RESERVE_LOW"}}`, 4},
		{"missing readiness", `{"contractVersion":1,"requestId":"health","data":{"status":"ready"}}`, 4},
		{"contradictory status", `{"contractVersion":1,"data":{"status":"unavailable","ready":true}}`, 4},
		{"failure reason", `{"contractVersion":1,"data":{"status":"ready","ready":true,"reasonCode":"SERVICE_UNAVAILABLE"}}`, 4},
		{"unsupported version", `{"contractVersion":2,"data":{"status":"ready","ready":true}}`, 4},
		{"malformed", `not json`, 4},
		{"duplicate ready", `{"contractVersion":1,"data":{"status":"ready","ready":false,"ready":true}}`, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Keep socket paths below the platform limit even under a long macOS test directory.
			dir, err := os.MkdirTemp("/tmp", "dm-health-")
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
				if r.Method != "GET" || r.URL.Path != "/api/v1/health" {
					t.Error(r.Method, r.URL.Path)
				}
				_, _ = io.WriteString(w, tc.body)
			})}
			go server.Serve(listener)
			defer server.Close()
			var out, diagnostic bytes.Buffer
			if code := run([]string{"--socket=" + socket, "health"}, nil, &out, &diagnostic); code != tc.code {
				t.Fatalf("exit=%d want=%d", code, tc.code)
			}
			if out.String() != tc.body {
				t.Fatal("health receipt was lost")
			}
			if tc.code == 0 && diagnostic.Len() != 0 {
				t.Fatal(diagnostic.String())
			}
		})
	}
}
