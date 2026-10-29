package service

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Both import endpoints reject malformed input before observing inventory or mutating configuration.
func TestConfigurationImportServiceGuards(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerConfigurationImport(mux, m)
	for _, action := range []string{"preview", "apply"} {
		for _, tc := range []struct {
			method, query, owner string
			want                 int
		}{
			{"GET", "", "admin", 405}, {"POST", "?x=1", "admin", 400}, {"POST", "", "", 400}, {"POST", "", "admin", 400},
		} {
			r := httptest.NewRequest(tc.method, "/api/v1/configuration-import-"+action+tc.query, strings.NewReader(`{}`))
			r.Header.Set("X-DiskMap-Principal", tc.owner)
			r.Header.Set("Idempotency-Key", "key")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(action, w.Code, w.Body.String())
			}
		}
	}
}
