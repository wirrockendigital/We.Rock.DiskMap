package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Binary map replies remain buffered until the final DSM identity check has succeeded.
func TestMapIndexBinaryGuard(t *testing.T) {
	body := `{"contractVersion":1,"profileId":"p","viewId":"v","indexId":"` + strings.Repeat("a", 64) + `","blocks":[0],"query":{"snapshotId":"s","rootId":"nas","metric":"logical","depth":1,"elementBudget":1,"filter":{"categories":[]}}}`
	payload := "WRDCH003" + strings.Repeat("x", 128<<10)
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/map-chunks" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" {
			t.Fatal("unqualified binary route")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/octet-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}}
	request := scanRequest(body)
	request.URL.RawQuery = "action=map-chunks"
	w := httptest.NewRecorder()
	b.ServeHTTP(w, request)
	if w.Code != 200 || w.Body.String() != payload || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.Len())
	}
	checks := 0
	b.identity = func(context.Context) (string, error) {
		checks++
		if checks >= 3 {
			return "changed-admin", nil
		}
		return "verified-admin", nil
	}
	request = scanRequest(body)
	request.URL.RawQuery = "action=map-chunks"
	w = httptest.NewRecorder()
	b.ServeHTTP(w, request)
	if w.Code == 200 || strings.Contains(w.Body.String(), "WRDCH003") {
		t.Fatal("late principal change leaked binary data")
	}
}
