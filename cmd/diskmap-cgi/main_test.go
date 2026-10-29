package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

type transportFunc func(*http.Request) (*http.Response, error)

// RoundTrip allows a deterministic logout during a pending backend response.
func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSessionRevocation prevents disclosure even after the backend completed work.
func TestSessionRevocation(t *testing.T) {
	valid := true
	backendCalls := 0
	b := bridge{identity: func(context.Context) (string, error) {
		if !valid {
			return "", dsmauth.ErrSession
		}
		return "test-admin", nil
	}, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
		backendCalls++
		valid = false
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"private":"must not escape"}`))}, nil
	})}}
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, authorizedRequest("GET", "/api.cgi?action=health"))
	if recorder.Code != 401 || strings.Contains(recorder.Body.String(), "private") || backendCalls != 1 {
		t.Fatalf("revoked session disclosed data: status=%d calls=%d", recorder.Code, backendCalls)
	}
	// A subsequent request must fail before touching the backend.
	b.ServeHTTP(httptest.NewRecorder(), authorizedRequest("GET", "/api.cgi?action=health"))
	if backendCalls != 1 {
		t.Fatal("unauthenticated request reached backend")
	}
}

// TestBackendBoundaries rejects oversized and malformed responses without disclosure.
func TestBackendBoundaries(t *testing.T) {
	for _, value := range []struct {
		body string
		code int
	}{{`{"ready":false}`, 200}, {"not-json", 502}, {`{"data":"` + strings.Repeat("x", 65536) + `"}`, 502}} {
		b := bridge{identity: func(context.Context) (string, error) { return "test-admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(value.body))}, nil
		})}}
		recorder := httptest.NewRecorder()
		b.ServeHTTP(recorder, authorizedRequest("GET", "/api.cgi?action=health"))
		if recorder.Code != value.code || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected guarded response: %d", recorder.Code)
		}
	}
}

// authorizedRequest supplies only a synthetic token; actual vendor verification is a separate DSM test.
func authorizedRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("X-SYNO-TOKEN", "synthetic-test-token")
	return r
}

// Unknown UI, download and status actions never become arbitrary backend or filesystem access.
func TestRouteBoundary(t *testing.T) {
	for _, failure := range []error{dsmauth.ErrSession, dsmauth.ErrRole, nil} {
		for _, action := range []string{"session", "health", "diagnostics", "status", "download", "ui", "../../control.db"} {
			calls := 0
			b := bridge{identity: func(context.Context) (string, error) {
				if failure != nil {
					return "", failure
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"bootstrap"}`))}, nil
			})}}
			recorder := httptest.NewRecorder()
			b.ServeHTTP(recorder, authorizedRequest("GET", "/api.cgi?action="+action))
			expected := 404
			if action == "session" || action == "health" || action == "diagnostics" {
				expected = 200
			}
			if failure == dsmauth.ErrSession {
				expected = 401
			}
			if failure == dsmauth.ErrRole {
				expected = 403
			}
			if recorder.Code != expected {
				t.Fatal(action, recorder.Code, expected)
			}
			if failure != nil && (calls != 0 || strings.Contains(recorder.Body.String(), "bootstrap")) {
				t.Fatal("denied request reached backend")
			}
		}
	}
}

// TestReadRoutes ensures that only typed identifiers reach the fixed backend routes.
func TestReadRoutes(t *testing.T) {
	for _, tc := range []struct {
		query, path string
		status      int
	}{
		{"action=profiles", "/api/v1/profiles", 200},
		{"action=job&id=job-1", "/api/v1/jobs/job-1", 200},
		{"action=scan-boundaries&profileId=p&jobId=j", "/api/v1/profiles/p/scan-boundaries?jobId=j", 200},
		{"action=scan-boundaries&profileId=p&jobId=j&path=/etc", "", 400},
		{"action=snapshot&profileId=p1", "/api/v1/snapshot?profileId=p1", 200},
		{"action=filestation&profileId=p&snapshotId=s&nodeId=n&viewId=v", "/api/v1/filestation?nodeId=n&profileId=p&snapshotId=s&viewId=v", 200},
		{"action=filestation&profileId=p&snapshotId=s&nodeId=n", "", 400},
		{"action=filestation&profileId=p&snapshotId=s&nodeId=n&viewId=v&viewId=w", "", 400},
		{"action=node-paths&profileId=p&snapshotId=s&nodeId=n", "/api/v1/node-paths?nodeId=n&profileId=p&snapshotId=s", 200},
		{"action=node-paths&profileId=p&snapshotId=s&nodeId=n&path=/etc", "", 400},
		{"action=job&id=..%2Fetc", "", 400}, {"action=job&id=x&id=y", "", 400},
		{"action=snapshot&profileId=p&jobId=j", "/api/v1/snapshot?jobId=j&profileId=p", 200},
		{"action=snapshot&profileId=p&jobId=j&jobId=k", "", 400},
		{"action=snapshot&profileId=p&jobId=", "", 400},
		{"action=snapshot&profileId=p&path=/etc", "", 400}, {"action=filestation&profileId=p&snapshotId=s", "", 400},
	} {
		calls := 0
		b := bridge{identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			// File Station receives only the identity established by the CGI session guard.
			if strings.HasPrefix(tc.path, "/api/v1/filestation?") && r.Header.Get("X-DiskMap-Principal") != "admin" {
				t.Fatal("missing guarded principal")
			}
			if r.URL.RequestURI() != tc.path {
				t.Fatal(r.URL.RequestURI(), tc.path)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{}}`))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?"+tc.query))
		if w.Code != tc.status || (tc.status != 200 && calls != 0) {
			t.Fatal(tc.query, w.Code, calls)
		}
	}
}
