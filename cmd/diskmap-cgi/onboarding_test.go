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

// The real CGI boundary owns principals, rechecks roles and bounds every template response.
func TestOnboardingCGIGuards(t *testing.T) {
	good := `{"contractVersion":1,"expectedRevision":0,"change":{"operation":"replace","draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"}}}`
	calls := 0
	revoked := false
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
		if revoked {
			return "", dsmauth.ErrSession
		}
		return "verified-admin", nil
	}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Fatal("unsafe identity")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"version":1,"revision":1,"draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"},"pendingProfileCreate":null}}`))}, nil
	})}}
	for _, body := range []string{strings.Replace(good, "change", "Change", 1), strings.Replace(good, `"change":`, `"dsmUserId":"foreign","change":`, 1), strings.Repeat(" ", 32769)} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=onboarding-draft"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=onboarding-draft"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=onboarding-draft"
	r.Header.Set("X-DiskMap-Principal", "forged")
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	b.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		revoked = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"secret":"must be withheld"}`))}, nil
	})
	w = httptest.NewRecorder()
	b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=onboarding-draft"))
	if w.Code != 401 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A backend conflict must preserve its exact fixed code; arbitrary or oversized replies are rejected.
func TestOnboardingCGIResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{
		{409, `{"contractVersion":1,"error":{"code":"ONBOARDING_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"IDEMPOTENCY_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"private path"}}`, 502},
		{200, `{"contractVersion":1,"data":{"version":1,"dsmUserId":"foreign"}}`, 502},
		{200, `{"data":"` + strings.Repeat("x", 65536) + `"}`, 502},
	} {
		b := bridge{identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=onboarding-draft"))
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Mutation success must attest the unchanged draft and expected revision, not merely a valid DTO.
func TestOnboardingReceiptBinding(t *testing.T) {
	in := []byte(`{"contractVersion":1,"expectedRevision":0,"change":{"operation":"replace","draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"}}}`)
	good := `{"contractVersion":1,"data":{"version":1,"revision":1,"draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"},"pendingProfileCreate":null}}`
	if !validOnboardingResponse([]byte(good), in) {
		t.Fatal("valid receipt")
	}
	for _, bad := range []string{strings.Replace(good, `"revision":1`, `"revision":2`, 1), strings.Replace(good, `"language":"de"`, `"language":"en"`, 1)} {
		if validOnboardingResponse([]byte(bad), in) {
			t.Fatal("unbound receipt")
		}
	}
}
