package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The source picker receives only a strictly decoded, path-free catalogue from its own session.
func TestScopeCGIProjectionGuards(t *testing.T) {
	good := `{"contractVersion":1,"requestId":"scope","data":{"volumes":[{"id":"volume-a","name":"Volume","state":"reachable"}],"shares":[],"observedAt":"2026-10-10T00:00:00Z"}}`
	for _, tc := range []struct {
		name, body string
		revoke     bool
		status     int
	}{
		{"valid", good, false, 200},
		{"private-field", strings.Replace(good, `"name":"Volume"`, `"name":"Volume","mountPath":"/private"`, 1), false, 502},
		{"invalid-state", strings.Replace(good, "reachable", "fake", 1), false, 502},
		{"role-lost", good, true, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revoked := false
			b := bridge{identity: func(context.Context) (string, error) {
				if revoked {
					return "", dsmauth.ErrSession
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/v1/scope-inventory" || r.Header.Get("X-DiskMap-Principal") != "admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
					t.Fatal("unsafe bridge")
				}
				revoked = tc.revoke
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			w := httptest.NewRecorder()
			b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=scope-inventory"))
			if w.Code != tc.status || strings.Contains(w.Body.String(), "/private") || tc.revoke && strings.Contains(w.Body.String(), "Volume") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// The explicit preview budget grows only this read-only route; the shared guard stays bounded.
func TestScopePreviewCGILargeRequest(t *testing.T) {
	ids := []string{}
	for i := 0; i < 1536; i++ {
		ids = append(ids, fmt.Sprintf("id-%04d-%s", i, strings.Repeat("a", 118)))
	}
	raw, _ := json.Marshal(map[string]any{"contractVersion": 1, "scope": map[string]any{"kind": "mixed", "targetIds": ids}})
	if len(raw) <= 16384 || len(raw) > 256<<10 {
		t.Fatal("invalid large fixture", len(raw))
	}
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if len(body) <= 16384 {
			t.Fatal("request truncated")
		}
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"error":{"code":"SCOPE_CHANGED"}}`))}, nil
	})}}
	for _, tc := range []struct {
		body string
		want int
	}{{string(raw), 409}, {strings.Repeat(" ", 256<<10+1), 400}} {
		r := scanRequest(tc.body)
		r.URL.RawQuery = "action=scope-preview"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("oversized body forwarded", calls)
	}
}

// Same-origin protection applies to explicit preflight although it never changes source data.
func TestScopePreviewCGIGuards(t *testing.T) {
	good := `{"contractVersion":1,"scope":{"kind":"nas","targetIds":[]}}`
	calls := 0
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"error":{"code":"SCOPE_CHANGED"}}`))}, nil
	})}}
	for _, tc := range []struct {
		body, origin string
		status       int
	}{
		{good, "https://foreign.test", 403},
		{strings.Replace(good, `"nas"`, `"arbitrary"`, 1), "https://nas.test:5001", 400},
		{strings.Replace(good, `"scope":`, `"path":"/private","scope":`, 1), "https://nas.test:5001", 400},
		{good, "https://nas.test:5001", 409},
	} {
		r := scanRequest(tc.body)
		r.URL.RawQuery = "action=scope-preview"
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("invalid request reached root preflight", calls)
	}
}
