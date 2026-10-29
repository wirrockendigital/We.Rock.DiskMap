package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLeasedPathsTransportGuard keeps this small response inside the shared final session check.
func TestLeasedPathsTransportGuard(t *testing.T) {
	body := `{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","nodeId":"node"}`
	payload := `{"contractVersion":1,"data":{"defaultMetric":"logical"}}`
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/leased-paths" || r.Header.Get("X-DiskMap-Principal") != "admin" {
			t.Fatal("wrong forwarding")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}}
	call := func(raw string) *httptest.ResponseRecorder {
		r := scanRequest(raw)
		w := httptest.NewRecorder()
		r.URL.RawQuery = "action=leased-paths"
		b.ServeHTTP(w, r)
		return w
	}
	w := call(body)
	if w.Code != 200 || w.Body.String() != payload {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(strings.Replace(body, `"nodeId":"node"`, `"nodeId":"node","qualified":true`, 1))
	if w.Code != 400 || calls != 1 {
		t.Fatal("client qualification accepted", w.Code, calls)
	}
	payload = `{"data":"` + strings.Repeat("x", 65537) + `"}`
	w = call(body)
	if w.Code != 502 {
		t.Fatal("unbounded response accepted", w.Code)
	}
	payload = `{"contractVersion":1,"data":{"private":"secret-marker"}}`
	checks := 0
	b.identity = func(context.Context) (string, error) {
		checks++
		if checks >= 3 {
			return "other", nil
		}
		return "admin", nil
	}
	w = call(body)
	if w.Code == 200 || strings.Contains(w.Body.String(), "secret-marker") {
		t.Fatal("late role change leaked payload", w.Code)
	}
}
