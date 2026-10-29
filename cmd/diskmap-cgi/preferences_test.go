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

// The real CGI boundary owns principals, rechecks roles and bounds every preference response.
func TestPreferencesCGIGuards(t *testing.T) {
	good := `{"contractVersion":2,"expectedRevision":0,"group":"appearance","appearance":{"language":"de","theme":"auto","map":"treemap","locale":null,"timeZone":null}}`
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
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"version":2,"display":{"units":"decimal","colorMode":"folder","detail":"auto","labels":true,"percentages":true,"hierarchyBorders":"normal","reducedMotion":"auto"},"dsmUserId":"verified-admin","revision":1,"language":"de","theme":"auto","map":"treemap","locale":null,"timeZone":null,"preferredProfileId":null,"lastView":null}}`))}, nil
	})}}
	for _, body := range []string{strings.Replace(good, "language", "Language", 1), strings.Replace(good, `"group":`, `"dsmUserId":"foreign","group":`, 1), strings.Repeat(" ", 16385)} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=preferences"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=preferences"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=preferences"
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
	b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=preferences"))
	if w.Code != 401 || strings.Contains(w.Body.String(), "secret") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A backend conflict must preserve its exact fixed code; arbitrary or oversized replies are rejected.
func TestPreferencesCGIResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   int
	}{
		{409, `{"contractVersion":1,"error":{"code":"PREFERENCE_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"IDEMPOTENCY_CONFLICT"}}`, 409},
		{409, `{"contractVersion":1,"error":{"code":"private path"}}`, 502},
		{200, `{"contractVersion":1,"data":{"version":1,"dsmUserId":"foreign"}}`, 502},
		{200, `{"data":"` + strings.Repeat("x", 65536) + `"}`, 502},
	} {
		b := bridge{identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=preferences"))
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Identity is checked again after body decoding, before any storage side effect.
func TestPreferencesRoleLossBeforeWrite(t *testing.T) {
	calls, checks := 0, 0
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
		checks++
		if checks > 1 {
			return "", dsmauth.ErrRole
		}
		return "admin", nil
	}, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, io.EOF })}}
	r := scanRequest(`{"contractVersion":2,"expectedRevision":0,"group":"preferredProfile","preferredProfile":{"profileId":null}}`)
	r.URL.RawQuery = "action=preferences"
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
}

// Current receipts must contain every display field and match the exact mutation intent.
func TestPreferenceDisplayCGIReceiptBinding(t *testing.T) {
	display := `{"units":"binary","colorMode":"filetype","detail":8,"labels":false,"percentages":false,"hierarchyBorders":"subtle","reducedMotion":"reduce"}`
	request := []byte(`{"contractVersion":2,"expectedRevision":4,"group":"display","display":` + display + `}`)
	good := `{"contractVersion":1,"data":{"version":2,"dsmUserId":"admin","revision":5,"language":"auto","theme":"auto","map":"treemap","locale":null,"timeZone":null,"preferredProfileId":null,"lastView":null,"display":` + display + `}}`
	if !validPreferenceResponse([]byte(good), "admin", request) {
		t.Fatal("valid current receipt")
	}
	for _, body := range []string{
		strings.Replace(good, `"version":2`, `"version":1`, 1), strings.Replace(good, `"revision":5`, `"revision":4`, 1),
		strings.Replace(good, `"units":"binary"`, `"units":"decimal"`, 1), strings.Replace(good, `"labels":false,`, ``, 1),
		strings.Replace(good, `"dsmUserId":"admin"`, `"dsmUserId":"foreign"`, 1), strings.Replace(good, `"labels":false`, `"labels":null`, 1),
	} {
		if validPreferenceResponse([]byte(body), "admin", request) {
			t.Fatal("unqualified receipt accepted", body)
		}
	}
	// GET validates the current complete DTO without pretending to have a mutation receipt.
	if !validPreferenceResponse([]byte(good), "admin", nil) {
		t.Fatal("valid current read")
	}
}
