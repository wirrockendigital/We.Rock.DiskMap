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
func TestExclusionTemplateHeadersAndStrictSchema(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerExclusionTemplate(mux, m)
	good := `{"contractVersion":1,"expectedRevision":0,"rules":[{"kind":"pattern","value":"*.tmp"}]}`
	for _, tc := range []struct {
		body, path string
		principals []string
		want       int
	}{
		{good, "/api/v1/exclusion-template", nil, 400}, {good, "/api/v1/exclusion-template", []string{"a", "b"}, 400},
		{good, "/api/v1/exclusion-template?principal=b", []string{"a"}, 400},
		{strings.Replace(good, `"rules"`, `"Rules"`, 1), "/api/v1/exclusion-template", []string{"a"}, 400},
		{strings.Replace(good, `"rules":`, `"dsmUserId":"b","rules":`, 1), "/api/v1/exclusion-template", []string{"a"}, 400},
		{strings.Repeat(" ", 16385), "/api/v1/exclusion-template", []string{"a"}, 400},
		{good, "/api/v1/exclusion-template", []string{"a"}, 200},
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
		r := httptest.NewRequest("GET", "/api/v1/exclusion-template", nil)
		r.Header.Set("X-DiskMap-Principal", p)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || p == "b" && strings.Contains(w.Body.String(), `"language":"de"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Actual HTTP receipts preserve both conflict classes and cancelled reads never manufacture defaults.
func TestExclusionTemplateServiceConflictsAndCancellation(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerExclusionTemplate(mux, m)
	good := `{"contractVersion":1,"expectedRevision":0,"rules":[{"kind":"pattern","value":"*.tmp"}]}`
	for _, tc := range []struct {
		body, key string
		want      int
		code      string
	}{{good, "key", 200, ""}, {good, "key", 200, ""}, {good, "new", 409, "TEMPLATE_CONFLICT"}, {strings.Replace(good, `"*.tmp"`, `"*.bak"`, 1), "key", 409, "IDEMPOTENCY_CONFLICT"}} {
		r := httptest.NewRequest("POST", "/api/v1/exclusion-template", strings.NewReader(tc.body))
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
	r := httptest.NewRequest("GET", "/api/v1/exclusion-template", nil).WithContext(ctx)
	r.Header.Set("X-DiskMap-Principal", "new-user")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"revision":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}
