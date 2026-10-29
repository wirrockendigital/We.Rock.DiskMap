package main

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const searchBody = `{"contractVersion":1,"profileId":"p","viewId":"v","query":{"snapshotId":"s","rootId":"nas","scope":"recursive","text":"literal","field":"both","metric":"logical","sort":"name","direction":"asc","limit":50}}`

// Search shares the authenticated same-origin guard and forwards only the trusted principal.
func TestSearchCGIGuardsAndResponseBudget(t *testing.T) {
	calls := 0
	body := `{"contractVersion":1,"data":{"padding":"` + strings.Repeat("x", 128<<10) + `"}}`
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/search" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" {
			t.Fatal("unbound route/principal")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	perform := func(text, selector, origin string) *httptest.ResponseRecorder {
		r := scanRequest(text)
		r.URL.RawQuery = selector
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	w := perform(searchBody, "action=search", "https://nas.test:5001")
	if w.Code != 200 || w.Body.String() != body || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.Len())
	}
	for _, tc := range []struct {
		body, selector, origin string
		status                 int
	}{{searchBody, "action=search&path=/private", "https://nas.test:5001", 400}, {searchBody, "action=search", "https://foreign.test", 403}, {strings.Replace(searchBody, `"query":{`, `"query":{"path":"/private",`, 1), "action=search", "https://nas.test:5001", 400}, {strings.Replace(searchBody, `"scope":"recursive"`, `"scope":"arbitrary"`, 1), "action=search", "https://nas.test:5001", 400}, {strings.Repeat(" ", 65537), "action=search", "https://nas.test:5001", 400}} {
		w = perform(tc.body, tc.selector, tc.origin)
		if w.Code != tc.status || calls != 1 {
			t.Fatal(w.Code, tc.status, calls, w.Body.String())
		}
	}
	body = `{"padding":"` + strings.Repeat("x", query.MaxResponseBytes+4096) + `"}`
	w = perform(searchBody, "action=search", "https://nas.test:5001")
	if w.Code != 502 || strings.Contains(w.Body.String(), "padding") {
		t.Fatal("oversize response admitted", w.Code)
	}
	// Session revocation during the backend read must erase the buffered payload.
	body = `{"contractVersion":1,"data":{"private":"not-visible"}}`
	checks := 0
	b.identity = func(context.Context) (string, error) {
		checks++
		if checks >= 3 {
			return "", dsmauth.ErrSession
		}
		return "verified-admin", nil
	}
	w = perform(searchBody, "action=search", "https://nas.test:5001")
	if w.Code == 200 || strings.Contains(w.Body.String(), "not-visible") {
		t.Fatal("revoked session leaked result", w.Code)
	}
}
