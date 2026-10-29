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

// The strict projection and final role check protect a read even when the private backend misbehaves.
func TestStatusSnapshotCGI(t *testing.T) {
	good := `{"contractVersion":1,"data":{"observedAt":"2026-10-10T00:00:00Z","active":null,"queued":[]}}`
	for _, tc := range []struct {
		body   string
		revoke bool
		want   int
	}{{good, false, 200}, {good, true, 401}, {strings.Replace(good, `"queued":[]`, `"queued":null`, 1), false, 502}, {strings.Replace(good, `"active":null`, `"privatePath":"/secret","active":null`, 1), false, 502}, {strings.Repeat(" ", 65537), false, 502}} {
		revoked := false
		b := bridge{identity: func(context.Context) (string, error) {
			if revoked {
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/status-snapshot" || r.Method != "GET" {
				t.Fatal(r.URL)
			}
			revoked = tc.revoke
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/?action=status-snapshot", nil)
		req.Header.Set("X-SYNO-TOKEN", "test")
		b.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Real route admission rejects malformed selectors, unsupported methods and invalid sessions before forwarding.
func TestStatusSnapshotCGIAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, method, query, token string
		identityError              error
		want                       int
	}{
		{"missing-token", "GET", "action=status-snapshot", "", nil, 401},
		{"expired", "GET", "action=status-snapshot", "test", dsmauth.ErrSession, 401},
		{"non-admin", "GET", "action=status-snapshot", "test", dsmauth.ErrRole, 403},
		{"post", "POST", "action=status-snapshot", "test", nil, 405},
		{"delete", "DELETE", "action=status-snapshot", "test", nil, 405},
		{"extra-profile", "GET", "action=status-snapshot&profileId=p", "test", nil, 400},
		{"extra-job", "GET", "action=status-snapshot&id=j", "test", nil, 400},
		{"duplicate-action", "GET", "action=status-snapshot&action=status-snapshot", "test", nil, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bridge{identity: func(context.Context) (string, error) { return "admin", tc.identityError }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected forwarding"); return nil, nil })}}
			req := httptest.NewRequest(tc.method, "/?"+tc.query, nil)
			if tc.token != "" {
				req.Header.Set("X-SYNO-TOKEN", tc.token)
			}
			w := httptest.NewRecorder()
			b.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
