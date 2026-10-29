package main

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The read bridge accepts only both exact scope identifiers and no client principal override.
func TestScanPreviewQueryBoundary(t *testing.T) {
	for _, query := range []struct {
		raw   string
		valid bool
	}{
		{"action=scan-preview&jobId=job&profileId=profile", true},
		{"action=scan-preview&jobId=job", false},
		{"action=scan-preview&jobId=job&profileId=profile&principal=admin", false},
		{"action=scan-preview&jobId=job&jobId=other&profileId=profile", false},
	} {
		action, err := urlQuery(httptest.NewRequest("GET", "/?"+query.raw, nil))
		if (err == nil) != query.valid || query.valid && action != "scan-preview" {
			t.Fatal(query, action, err)
		}
	}
}

// The complete preview envelope passes both guards above 64 KiB, with fresh principal checks.
func TestScanPreviewGuardedEnvelope(t *testing.T) {
	segments := make([]scanner.PreviewSegment, 64)
	for i := range segments {
		segments[i] = scanner.PreviewSegment{ID: "preview-only", Name: strings.Repeat("n", 256), Path: strings.Repeat("p", 768), Kind: "file", Bytes: "1"}
	}
	encoded, err := json.Marshal(map[string]any{"contractVersion": 1, "requestId": "preview", "data": map[string]any{"jobId": "job", "profileId": "profile", "preview": scanner.Preview{Sequence: 1, CapturedAt: "2026-10-14T12:00:00Z", Entries: "64", KnownLogicalBytes: "64", Segments: segments}}})
	if err != nil || len(encoded) <= 64*1024 || len(encoded) >= 120*1024 {
		t.Fatal("fixture budget", len(encoded), err)
	}
	for _, revoke := range []bool{false, true} {
		valid := true
		checks := 0
		calls := 0
		b := bridge{identity: func(context.Context) (string, error) {
			checks++
			if !valid {
				return "", dsmauth.ErrSession
			}
			return "actual-admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("X-DiskMap-Principal") != "actual-admin" || r.URL.Path != "/api/v1/scan-preview" || r.URL.Query().Get("jobId") != "job" || r.URL.Query().Get("profileId") != "profile" {
				t.Fatal("scope/principal mismatch", r.URL, r.Header)
			}
			if revoke {
				valid = false
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(encoded)))}, nil
		})}}
		request := authorizedRequest("GET", "/api.cgi?action=scan-preview&jobId=job&profileId=profile")
		request.Header.Set("X-DiskMap-Principal", "spoofed")
		response := httptest.NewRecorder()
		b.ServeHTTP(response, request)
		if calls != 1 || checks < 2 {
			t.Fatal("missing fresh authentication", calls, checks)
		}
		if revoke {
			if response.Code != 401 || strings.Contains(response.Body.String(), "preview-only") {
				t.Fatal("revoked preview disclosed", response.Code)
			}
		} else if response.Code != 200 || response.Body.String() != string(encoded) {
			t.Fatal("bounded preview rejected", response.Code, response.Body.Len())
		}
	}
}
