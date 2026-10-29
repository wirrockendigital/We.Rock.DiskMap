package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLeasedPathsStrictBoundary rejects private paths, case drift and absent principals before manager access.
func TestLeasedPathsStrictBoundary(t *testing.T) {
	mux := http.NewServeMux()
	registerLeasedPaths(mux, nil)
	body := `{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","nodeId":"node"}`
	for _, tc := range []struct{ body, principal string }{{body, ""}, {strings.Replace(body, `"nodeId":"node"`, `"nodeId":"node","path":"/private"`, 1), "admin"}, {strings.Replace(body, "profileId", "ProfileId", 1), "admin"}, {strings.Replace(body, `"contractVersion":1`, `"contractVersion":2`, 1), "admin"}} {
		r := httptest.NewRequest("POST", "/api/v1/leased-paths", strings.NewReader(tc.body))
		if tc.principal != "" {
			r.Header.Set("X-DiskMap-Principal", tc.principal)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), "/private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
