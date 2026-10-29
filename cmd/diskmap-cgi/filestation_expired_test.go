package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFileStationExpiredLease preserves the recoverable lease contract through the real CGI route.
func TestFileStationExpiredLease(t *testing.T) {
	for _, test := range []struct {
		code string
		want int
	}{{"VIEW_EXPIRED", 409}, {"PRIVATE_UNKNOWN_CODE", 502}} {
		t.Run(test.code, func(t *testing.T) {
			calls := 0
			b := bridge{identity: func(context.Context) (string, error) { return "test-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/api/v1/filestation" || r.Header.Get("X-DiskMap-Principal") != "test-admin" || r.URL.Query().Get("viewId") != "expired-view" {
					t.Fatal("File Station request lost its authenticated lease binding")
				}
				body := `{"contractVersion":1,"error":{"code":"` + test.code + `","messageKey":"private-path","requestId":"private-principal","retryable":false}}`
				return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			w := httptest.NewRecorder()
			b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=filestation&profileId=profile&snapshotId=snapshot&nodeId=node&viewId=expired-view"))
			if calls != 1 || w.Code != test.want || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "PRIVATE_UNKNOWN_CODE") {
				t.Fatalf("unsafe or incorrect public failure: status=%d calls=%d", w.Code, calls)
			}
			if test.want == 409 && !strings.Contains(w.Body.String(), `"code":"VIEW_EXPIRED"`) {
				t.Fatal("expired lease became an unclassified gateway failure")
			}
		})
	}
}
