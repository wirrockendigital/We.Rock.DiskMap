package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReaderActionGuard prevents malformed or cross-origin lease requests from reaching the private service.
func TestReaderActionGuard(t *testing.T) {
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/views" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Fatal("unsafe principal transport")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"released":true}}`))}, nil
	})}}
	good := `{"contractVersion":1,"operation":"release","profileId":"p","viewId":"v"}`
	for _, body := range []string{strings.Replace(good, "profileId", "ProfileId", 1), strings.Replace(good, `"viewId":"v"`, `"viewId":"v","path":"/etc"`, 1), strings.Replace(good, `"release"`, `"unknown"`, 1)} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=view"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=view"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=view"
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
}
