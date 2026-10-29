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
func TestRegionalContextCGI(t *testing.T) {
	good := `{"contractVersion":1,"data":{"version":1,"nasTimeZone":"Europe/Amsterdam","timeZoneSource":"system-zoneinfo"}}`
	for _, tc := range []struct {
		body   string
		revoke bool
		want   int
	}{{good, false, 200}, {`{"contractVersion":1,"data":{"version":1,"nasTimeZone":null,"timeZoneSource":"unavailable"}}`, false, 200}, {strings.Replace(good, "Europe/Amsterdam", "../private", 1), false, 502}, {good, true, 401}, {strings.Replace(good, `"system-zoneinfo"`, `"unavailable"`, 1), false, 502}, {strings.Replace(good, `"version":1`, `"privatePath":"/secret","version":1`, 1), false, 502}, {strings.Repeat(" ", 4097), false, 502}} {
		revoked := false
		b := bridge{identity: func(context.Context) (string, error) {
			if revoked {
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/regional-context" || r.Method != "GET" {
				t.Fatal(r.URL)
			}
			revoked = tc.revoke
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/?action=regional-context", nil)
		req.Header.Set("X-SYNO-TOKEN", "test")
		b.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Real route admission rejects malformed selectors, unsupported methods and invalid sessions before forwarding.
func TestRegionalContextCGIAdmission(t *testing.T) {
	for _, tc := range []struct {
		name, method, query, token string
		identityError              error
		want                       int
	}{
		{"missing-token", "GET", "action=regional-context", "", nil, 401},
		{"expired", "GET", "action=regional-context", "test", dsmauth.ErrSession, 401},
		{"non-admin", "GET", "action=regional-context", "test", dsmauth.ErrRole, 403},
		{"post", "POST", "action=regional-context", "test", nil, 405},
		{"delete", "DELETE", "action=regional-context", "test", nil, 405},
		{"extra-profile", "GET", "action=regional-context&profileId=p", "test", nil, 400},
		{"extra-job", "GET", "action=regional-context&id=j", "test", nil, 400},
		{"duplicate-action", "GET", "action=regional-context&action=regional-context", "test", nil, 400},
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
