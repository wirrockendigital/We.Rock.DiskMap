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
func TestPreferencesHeadersAndStrictSchema(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerPreferences(mux, m)
	good := `{"contractVersion":2,"expectedRevision":0,"group":"appearance","appearance":{"language":"de","theme":"auto","map":"treemap","locale":null,"timeZone":null}}`
	for _, tc := range []struct {
		body, path string
		principals []string
		want       int
	}{
		{good, "/api/v1/preferences", nil, 400}, {good, "/api/v1/preferences", []string{"a", "b"}, 400},
		{good, "/api/v1/preferences?principal=b", []string{"a"}, 400},
		{strings.Replace(good, `"language"`, `"Language"`, 1), "/api/v1/preferences", []string{"a"}, 400},
		{strings.Replace(good, `"group":`, `"dsmUserId":"b","group":`, 1), "/api/v1/preferences", []string{"a"}, 400},
		{strings.Repeat(" ", 16385), "/api/v1/preferences", []string{"a"}, 400},
		{good, "/api/v1/preferences", []string{"a"}, 200},
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
		r := httptest.NewRequest("GET", "/api/v1/preferences", nil)
		r.Header.Set("X-DiskMap-Principal", p)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 || p == "b" && strings.Contains(w.Body.String(), `"language":"de"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Actual HTTP receipts preserve both conflict classes and cancelled reads never manufacture defaults.
func TestPreferencesServiceConflictsAndCancellation(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerPreferences(mux, m)
	good := `{"contractVersion":2,"expectedRevision":0,"group":"appearance","appearance":{"language":"de","theme":"auto","map":"treemap","locale":null,"timeZone":null}}`
	for _, tc := range []struct {
		body, key string
		want      int
		code      string
	}{{good, "key", 200, ""}, {good, "key", 200, ""}, {good, "new", 409, "PREFERENCE_CONFLICT"}, {strings.Replace(good, `"de"`, `"en"`, 1), "key", 409, "IDEMPOTENCY_CONFLICT"}} {
		r := httptest.NewRequest("POST", "/api/v1/preferences", strings.NewReader(tc.body))
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
	r := httptest.NewRequest("GET", "/api/v1/preferences", nil).WithContext(ctx)
	r.Header.Set("X-DiskMap-Principal", "new-user")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), `"revision":0`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

// Display writes use the existing principal/CAS route and reject old or incomplete bodies before mutation.
func TestPreferenceDisplayService(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerPreferences(mux, m)
	good := `{"contractVersion":2,"expectedRevision":0,"group":"display","display":{"units":"binary","colorMode":"filetype","detail":8,"labels":false,"percentages":true,"hierarchyBorders":"subtle","reducedMotion":"reduce"}}`
	for i, body := range []string{strings.Replace(good, `"contractVersion":2`, `"contractVersion":1`, 1), strings.Replace(good, `"labels":false,`, ``, 1), strings.Replace(good, `"labels":false`, `"labels":null`, 1), strings.Replace(good, `"colorMode":"filetype"`, `"colorMode":"mtime"`, 1), good} {
		r := httptest.NewRequest("POST", "/api/v1/preferences", strings.NewReader(body))
		r.Header.Set("X-DiskMap-Principal", "admin")
		r.Header.Set("Idempotency-Key", "same-unburned-key")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := 400
		if i == 4 {
			want = 200
		}
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		if i == 4 && (!strings.Contains(w.Body.String(), `"labels":false`) || !strings.Contains(w.Body.String(), `"version":2`)) {
			t.Fatal(w.Body.String())
		}
	}
}
