package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStorageActionGuard prevents malformed or cross-origin settings requests from reaching the private service.
func TestStorageActionGuard(t *testing.T) {
	calls := 0
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/storage/retention" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Fatal("unsafe principal transport")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"released":true}}`))}, nil
	})}}
	good := `{"contractVersion":1,"completeCount":2}`
	for _, body := range []string{strings.Replace(good, "completeCount", "CompleteCount", 1), `{"contractVersion":1,"completeCount":4}`, `{"contractVersion":1,"completeCount":2,"path":"/etc"}`} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=storage-retention"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=storage-retention"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=storage-retention"
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
}
