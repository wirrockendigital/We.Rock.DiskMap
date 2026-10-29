package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Export is a guarded read with fixed errors and no configuration mutation path.
func TestConfigurationExportService(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerConfigurationExport(mux, m)
	for _, tc := range []struct {
		method, path, principal string
		status                  int
	}{{"GET", "/api/v1/configuration-export", "admin", 200}, {"POST", "/api/v1/configuration-export", "admin", 405}, {"GET", "/api/v1/configuration-export?principal=other", "admin", 400}, {"GET", "/api/v1/configuration-export", "", 400}} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.principal != "" {
			req.Header.Set("X-DiskMap-Principal", tc.principal)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
		if tc.status == 200 {
			var v contracts.Envelope[contracts.ConfigurationExport]
			if json.Unmarshal(w.Body.Bytes(), &v) != nil || !jobs.ValidConfigurationBundle(v.Data.Bundle) {
				t.Fatal("invalid export")
			}
		}
	}
}

// The bundle limit is checked before writing any successful envelope, including JSON escaping.
func TestConfigurationExportServiceSize(t *testing.T) {
	out := contracts.ConfigurationExport{Bundle: contracts.ConfigurationBundle{ExportedAt: strings.Repeat("x", contracts.ConfigurationBundleLimit)}}
	w := httptest.NewRecorder()
	configurationExportReply(w, out)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "CONFIGURATION_TOO_LARGE") {
		t.Fatal(w.Code)
	}
}
