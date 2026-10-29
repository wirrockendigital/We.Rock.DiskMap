package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHierarchyTransportBudgetAndGuard verifies large responses still pass the final DSM role check.
func TestHierarchyTransportBudgetAndGuard(t *testing.T) {
	body := `{"contractVersion":1,"profileId":"p","viewId":"v","query":{"snapshotId":"s","rootId":"nas","metric":"logical","depth":2,"elementBudget":100,"filter":{"categories":[]}}}`
	large := `{"contractVersion":1,"data":{"padding":"` + strings.Repeat("x", 128<<10) + `"}}`
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/hierarchy" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" {
			t.Fatal("wrong bound transport")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(large))}, nil
	})}}
	r := scanRequest(body)
	r.URL.RawQuery = "action=hierarchy"
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != large || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.Len())
	}
	// Hostile selectors and extra source-path fields never reach the service.
	for _, tc := range []struct{ query, body string }{{"action=hierarchy&path=/etc", body}, {"action=hierarchy", strings.Replace(body, `"viewId":"v"`, `"viewId":"v","path":"/etc"`, 1)}} {
		r = scanRequest(tc.body)
		r.URL.RawQuery = tc.query
		w = httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 1 {
			t.Fatal(w.Code, calls)
		}
	}
	checks := 0
	b.identity = func(context.Context) (string, error) {
		checks++
		if checks >= 3 {
			return "changed-admin", nil
		}
		return "verified-admin", nil
	}
	r = scanRequest(body)
	r.URL.RawQuery = "action=hierarchy"
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code == 200 || strings.Contains(w.Body.String(), "padding") {
		t.Fatal("role change leaked response", w.Code)
	}
}
