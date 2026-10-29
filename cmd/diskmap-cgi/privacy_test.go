package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

// TestLogoutDuringBlockedQuery releases the backend only after session revocation.
func TestLogoutDuringBlockedQuery(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var revoked atomic.Bool
	var calls atomic.Int32
	b := bridge{identity: func(context.Context) (string, error) {
		if revoked.Load() {
			return "", dsmauth.ErrSession
		}
		return "test-admin", nil
	}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Scheme != "http" || r.URL.Host != "localhost" || r.Header.Get("X-SYNO-TOKEN") != "" || r.Header.Get("Cookie") != "" {
			t.Error("credential or destination escaped local bridge")
		}
		close(entered)
		<-release
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"privateName":"hidden"}}`))}, nil
	})}}
	w := httptest.NewRecorder()
	go func() { defer close(done); b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=profiles")) }()
	<-entered
	revoked.Store(true)
	close(release)
	<-done
	if w.Code != 401 || strings.Contains(w.Body.String(), "privateName") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("blocked query exposed revoked-session data")
	}
	again := httptest.NewRecorder()
	b.ServeHTTP(again, authorizedRequest("GET", "/api.cgi?action=profiles"))
	if again.Code != 401 || calls.Load() != 1 {
		t.Fatal("revoked session performed another query")
	}
}
