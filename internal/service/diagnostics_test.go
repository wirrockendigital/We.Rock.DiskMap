package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Native capabilities are qualified only for an actually read, previously measured DSM build.
func TestDiagnosticsVersionAndBoundary(t *testing.T) {
	dir := t.TempDir()
	m, e := jobs.Open(context.Background(), filepath.Join(dir, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	version := filepath.Join(dir, "VERSION")
	mux := http.NewServeMux()
	registerDiagnosticsVersion(mux, m, version)
	for _, tc := range []struct{ raw, want string }{{"buildnumber=\"90080\"\n", "qualified-probe"}, {"buildnumber=\"99999\"\n", "unqualified"}, {"buildnumber=\"private/path\"\n", "unknown"}} {
		if e = os.WriteFile(version, []byte(tc.raw), 0600); e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/diagnostics", nil))
		var out contracts.Envelope[jobs.DiagnosticStatus]
		if w.Code != 200 || len(w.Body.Bytes()) > 8192 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ContractVersion != 1 || out.Data.Capabilities.DSMFileStation != tc.want || out.Data.Capabilities.DSMScheduledScan != tc.want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/v1/diagnostics?profileId=private", "/api/v1/diagnostics?%invalid"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
