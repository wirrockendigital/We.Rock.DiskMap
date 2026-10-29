package main

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Folder reads retain same-origin validation, principal forwarding and final session revocation.
func TestScopeFoldersCGIGuards(t *testing.T) {
	good := `{"contractVersion":1,"requestId":"scope","data":{"shareId":"share-a","relativePath":".","entries":[{"name":"child","relativePath":"child"}],"nextCursor":null,"checkedAt":"2026-10-13T00:00:00Z"}}`
	for _, revoke := range []bool{false, true} {
		revoked := false
		calls := 0
		b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
			if revoked {
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != "/api/v1/scope-folders" || r.Header.Get("X-DiskMap-Principal") != "admin" {
				t.Fatal("route binding")
			}
			revoked = revoke
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(good))}, nil
		})}}
		r := scanRequest(`{"contractVersion":1,"shareId":"share-a","relativePath":".","after":null}`)
		r.URL.RawQuery = "action=scope-folders"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		want := 200
		if revoke {
			want = 401
		}
		if w.Code != want || calls != 1 || revoke && strings.Contains(w.Body.String(), "child") {
			t.Fatal(w.Code, calls)
		}
	}
}
