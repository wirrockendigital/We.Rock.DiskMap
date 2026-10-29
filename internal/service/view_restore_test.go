package service

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Restore is an exact bounded principal-owned request; an empty catalogue is an honest successful state.
func TestViewRestoreServiceBoundary(t *testing.T) {
	m, err := jobs.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerViewRestore(mux, m)
	good := `{"contractVersion":1,"viewId":"new-view","expectedPreferenceRevision":0}`
	for _, tc := range []struct {
		body       string
		principals []string
		want       int
	}{
		{good, nil, 400}, {good, []string{"a", "b"}, 400}, {good, []string{"admin"}, 200},
		{strings.Replace(good, "viewId", "ViewId", 1), []string{"admin"}, 400},
		{strings.Replace(good, `"contractVersion":`, `"owner":"foreign","contractVersion":`, 1), []string{"admin"}, 400},
		{strings.Replace(good, `"expectedPreferenceRevision":0`, `"expectedPreferenceRevision":1`, 1), []string{"admin"}, 409},
		{strings.Repeat(" ", 2049), []string{"admin"}, 400},
	} {
		r := httptest.NewRequest("POST", "/api/v1/views/restore", strings.NewReader(tc.body))
		r.Header["X-Diskmap-Principal"] = tc.principals
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "/api/v1/views/navigation", strings.NewReader(`{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","rootId":"nas","selectedNodeId":null}`))
	r.Header.Set("X-DiskMap-Principal", "admin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Fatal("unleased navigation accepted")
	}
}
