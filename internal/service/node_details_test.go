package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Unknown fields, selectors and missing owner context fail before opening any generation.
func TestNodeDetailsStrictRoute(t *testing.T) {
	mux := http.NewServeMux()
	registerNodeDetails(mux, nil)
	for _, body := range []string{`{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","rootId":"nas","nodeId":"n","metric":"logical","operation":"details","path":"/private"}`, `{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","rootId":"nas","nodeId":"n","metric":"logical","operation":"recursive-files","limit":21}`} {
		r := httptest.NewRequest("POST", "/api/v1/node-details", strings.NewReader(body))
		r.Header.Set("X-DiskMap-Principal", "admin")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
