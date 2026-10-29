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

// The new read route retains profile validation and the final session check of the common boundary.
func TestSchedulerStatusRoute(t *testing.T) {
	valid := true
	calls := 0
	b := bridge{identity: func(context.Context) (string, error) {
		if !valid {
			return "", dsmauth.ErrSession
		}
		return "admin", nil
	}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/scheduler-status" || r.URL.Query().Get("profileId") != "probe" {
			t.Fatal("wrong backend binding")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"statusVersion":1}}`))}, nil
	})}}
	for _, query := range []string{"scheduler-status&profileId=probe", "scheduler-status&profileId=probe&enabled=true", "scheduler-status&profileId=../escape"} {
		w := httptest.NewRecorder()
		b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action="+query))
		want := 400
		if query == "scheduler-status&profileId=probe" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls != 1 {
		t.Fatal("invalid route reached service")
	}
	valid = false
	w := httptest.NewRecorder()
	b.ServeHTTP(w, authorizedRequest("GET", "/api.cgi?action=scheduler-status&profileId=probe"))
	if w.Code != 401 || calls != 1 {
		t.Fatal("denied session queried status")
	}
}
