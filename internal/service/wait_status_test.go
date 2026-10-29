package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// The private projection rejects unknown jobs and extra query inputs without index access.
func TestWaitStatusRoute(t *testing.T) {
	m, e := jobs.Open(context.Background(), filepath.Join(t.TempDir(), "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerWaitStatus(mux, m)
	for _, path := range []string{"/api/v1/jobs/missing/wait-status", "/api/v1/jobs/missing/wait-status?enrich=true"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
