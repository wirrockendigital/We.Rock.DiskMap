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
func TestExclusionTemplateCGIGuards(t *testing.T) {
	good := `{"contractVersion":1,"expectedRevision":0,"rules":[{"kind":"pattern","value":"*.tmp"}]}`
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
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"version":1,"revision":1,"rules":[{"kind":"pattern","value":"*.tmp"}]}}`))}, nil
	})}}
	for _, body := range []string{strings.Replace(good, "rules", "Rules", 1), strings.Replace(good, `"rules":`, `"dsmUserId":"foreign","rules":`, 1), strings.Repeat(" ", 16385)} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=exclusion-template"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=exclusion-template"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=exclusion-template"
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
	b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=exclusion-template"))
	if w.Code != 401 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A backend conflict must preserve its exact fixed code; arbitrary or oversized replies are rejected.
func TestExclusionTemplateCGIResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{
		{409, `{"contractVersion":1,"error":{"code":"TEMPLATE_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"IDEMPOTENCY_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"private path"}}`, 502},
		{200, `{"contractVersion":1,"data":{"version":1,"dsmUserId":"foreign"}}`, 502},
		{200, `{"data":"` + strings.Repeat("x", 65536) + `"}`, 502},
	} {
		b := bridge{identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=exclusion-template"))
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// A successful mutation receipt must bind the exact expected revision and effective submitted rules.
func TestExclusionTemplateReceiptBinding(t *testing.T) {
	request := []byte(`{"contractVersion":1,"expectedRevision":7,"rules":[{"kind":"pattern","value":"*.tmp"}]}`)
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"contractVersion":1,"data":{"version":1,"revision":8,"rules":[{"kind":"pattern","value":"*.tmp"} ]}}`, true},
		{`{"contractVersion":1,"data":{"version":1,"revision":9,"rules":[{"kind":"pattern","value":"*.tmp"} ]}}`, false},
		{`{"contractVersion":1,"data":{"version":1,"revision":8,"rules":[]}}`, false},
	} {
		if got := validExclusionTemplateResponse([]byte(tc.body), request); got != tc.want {
			t.Fatal(got, tc.want)
		}
		if !validExclusionTemplateResponse([]byte(tc.body), nil) {
			t.Fatal("GET remains current-value read")
		}
	}
}
