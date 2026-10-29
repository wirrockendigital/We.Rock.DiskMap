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

// Typed bounded projection and the final session check guard even this read-only POST.
func TestProfileSummariesCGIBinding(t *testing.T) {
	good := `{"contractVersion":1,"data":{"observedAt":"2026-10-10T00:00:00Z","profiles":[{"profileId":"p","currentRevision":1,"snapshotId":null,"capturedRevision":null,"endedAt":null,"quality":"missing","freshness":"unknown","ageSeconds":null,"integrityQuality":"unknown","integrityCheckedAt":null,"lastAttempt":null}]}}`
	for _, tc := range []struct {
		body   string
		revoke bool
		want   int
	}{{good, false, 200},
		{strings.Replace(good, `"freshness":"unknown"`, `"freshness":"current"`, 1), false, 502},
		{strings.Replace(good, `"ageSeconds":null`, `"ageSeconds":"1"`, 1), false, 502},
		{strings.Replace(good, `"integrityQuality":"unknown"`, `"integrityQuality":"healthy"`, 1), false, 502}, {strings.Replace(good, `"profileId":"p"`, `"profileId":"other"`, 1), false, 502}, {good, true, 401}, {strings.Repeat(" ", 65537), false, 502}} {
		revoked := false
		b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
			if revoked {
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/profile-summaries" || r.Header.Get("X-DiskMap-Principal") != "admin" {
				t.Fatal("wrong route")
			}
			revoked = tc.revoke
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		r := scanRequest(`{"contractVersion":1,"profileIds":["p"]}`)
		r.URL.RawQuery = "action=profile-summaries"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// An authenticated session does not authorize a cross-origin metadata POST.
func TestProfileSummariesCGIOrigin(t *testing.T) {
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected forwarding"); return nil, nil })}}
	r := scanRequest(`{"contractVersion":1,"profileIds":["p"]}`)
	r.URL.RawQuery = "action=profile-summaries"
	r.Header.Set("Origin", "https://other.invalid")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
