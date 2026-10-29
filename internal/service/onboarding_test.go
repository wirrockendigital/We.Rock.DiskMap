package service

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The private transport rejects ambiguous owners and never accepts ownership from JSON or query parameters.
func TestOnboardingHeadersAndStrictSchema(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerOnboarding(mux, m)
	good := `{"contractVersion":1,"expectedRevision":0,"change":{"operation":"replace","draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"}}}`
	for _, tc := range []struct {
		body, path string
		principals []string
		want       int
	}{
		{good, "/api/v1/onboarding-draft", nil, 400}, {good, "/api/v1/onboarding-draft", []string{"a", "b"}, 400},
		{good, "/api/v1/onboarding-draft?principal=b", []string{"a"}, 400},
		{strings.Replace(good, `"change"`, `"Change"`, 1), "/api/v1/onboarding-draft", []string{"a"}, 400},
		{strings.Replace(good, `"change":`, `"dsmUserId":"b","change":`, 1), "/api/v1/onboarding-draft", []string{"a"}, 400},
		{strings.Repeat(" ", 32769), "/api/v1/onboarding-draft", []string{"a"}, 400},
		{good, "/api/v1/onboarding-draft", []string{"a"}, 200},
	} {
		r := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		r.Header["X-Diskmap-Principal"] = tc.principals
		r.Header.Set("Idempotency-Key", "key")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
	for _, p := range []string{"a", "b"} {
		r := httptest.NewRequest("GET", "/api/v1/onboarding-draft", nil)
		r.Header.Set("X-DiskMap-Principal", p)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || p == "b" && strings.Contains(w.Body.String(), `"language":"de"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Actual HTTP receipts preserve both conflict classes and cancelled reads never manufacture defaults.
func TestOnboardingServiceConflictsAndCancellation(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerOnboarding(mux, m)
	good := `{"contractVersion":1,"expectedRevision":0,"change":{"operation":"replace","draft":{"step":1,"language":"de","configuration":null,"profileId":null,"updateChoice":"undecided"}}}`
	for _, tc := range []struct {
		body, key string
		want      int
		code      string
	}{{good, "key", 200, ""}, {good, "key", 200, ""}, {good, "new", 409, "ONBOARDING_CONFLICT"}, {strings.Replace(good, `"de"`, `"en"`, 1), "key", 409, "IDEMPOTENCY_CONFLICT"}} {
		r := httptest.NewRequest("POST", "/api/v1/onboarding-draft", strings.NewReader(tc.body))
		r.Header.Set("X-DiskMap-Principal", "admin")
		r.Header.Set("Idempotency-Key", tc.key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/v1/onboarding-draft", nil).WithContext(ctx)
	r.Header.Set("X-DiskMap-Principal", "new-user")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"revision":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Large unfinished drafts use the dedicated 32-KiB operation without widening profile actions.
func TestOnboardingDedicatedRequestBudget(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerOnboarding(mux, m)
	rules := []string{}
	for i := 0; i < 5; i++ {
		rules = append(rules, `{"kind":"pattern","value":"`+strings.Repeat("x", 3500)+`"}`)
	}
	body := `{"contractVersion":1,"expectedRevision":0,"change":{"operation":"replace","draft":{"step":2,"language":"de","profileId":null,"updateChoice":"undecided","configuration":{"exclusions":[` + strings.Join(rules, ",") + `]}}}}`
	if len(body) <= 16384 {
		t.Fatal("fixture too small")
	}
	r := httptest.NewRequest("POST", "/api/v1/onboarding-draft", strings.NewReader(body))
	r.Header.Set("X-DiskMap-Principal", "admin")
	r.Header.Set("Idempotency-Key", "large")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
