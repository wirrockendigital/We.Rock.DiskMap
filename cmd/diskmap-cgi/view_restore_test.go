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

// The real guarded route does not trust caller principals and withholds responses after logout.
func TestViewRestoreCGIBoundary(t *testing.T) {
	good := `{"contractVersion":1,"viewId":"new-view","expectedPreferenceRevision":0}`
	calls := 0
	revoked := false
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
		if revoked {
			return "", dsmauth.ErrSession
		}
		return "admin", nil
	}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("X-DiskMap-Principal") != "admin" {
			t.Fatal("caller owner accepted")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"preferenceRevision":0,"profileId":null,"resolvedView":null,"reader":null,"navigation":null,"reasons":["NO_PROFILE"]}}`))}, nil
	})}}
	for _, body := range []string{strings.Replace(good, "viewId", "ViewId", 1), strings.Replace(good, `"viewId":`, `"owner":"foreign","viewId":`, 1)} {
		r := scanRequest(body)
		r.URL.RawQuery = "action=view-restore"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
	r := scanRequest(good)
	r.URL.RawQuery = "action=view-restore"
	r.Header.Set("Origin", "https://foreign.test")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal(w.Code, calls)
	}
	r = scanRequest(good)
	r.URL.RawQuery = "action=view-restore"
	r.Header.Set("X-DiskMap-Principal", "foreign")
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls, w.Body.String())
	}
	b.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		revoked = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"private":"withhold"}`))}, nil
	})
	r = scanRequest(good)
	r.URL.RawQuery = "action=view-restore"
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 401 || strings.Contains(w.Body.String(), "withhold") {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A syntactically valid upstream response must still bind the requested preference revision.
func TestViewRestoreCGIResponseBinding(t *testing.T) {
	request := `{"contractVersion":1,"viewId":"new-view","expectedPreferenceRevision":0}`
	for _, body := range []string{`{"contractVersion":1,"data":{"preferenceRevision":1,"profileId":null,"resolvedView":null,"reader":null,"navigation":null,"reasons":[]}}`, `{"contractVersion":1,"data":{"preferenceRevision":0,"foreign":"private","reasons":[]}}`, strings.Repeat(" ", 65537)} {
		b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}}
		r := scanRequest(request)
		r.URL.RawQuery = "action=view-restore"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 502 || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
