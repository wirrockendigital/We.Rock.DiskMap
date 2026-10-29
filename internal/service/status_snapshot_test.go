package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Unknown selectors and methods are rejected before any manager access.
func TestStatusSnapshotRequest(t *testing.T) {
	mux := http.NewServeMux()
	registerStatusSnapshot(mux, nil)
	for _, tc := range []struct {
		method, url string
		status      int
	}{{"GET", "/api/v1/status-snapshot?profileId=p", 400}, {"POST", "/api/v1/status-snapshot", 405}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.url, nil))
		if w.Code != tc.status {
			t.Fatal(w.Code)
		}
	}
}
