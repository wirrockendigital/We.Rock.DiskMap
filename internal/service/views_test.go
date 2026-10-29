package service

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewsBoundary verifies real persisted empty state and fail-closed runtime capabilities.
func TestViewsBoundary(t *testing.T) {
	base := t.TempDir()
	version := filepath.Join(base, "VERSION")
	if err := os.WriteFile(version, []byte("majorversion=\"7\"\nbuildnumber=\"90080\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerViews(mux, m, version, filepath.Join(base, "missing-shares"))
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/v1/profiles", 200}, {"/api/v1/snapshot?profileId=missing", 404}, {"/api/v1/snapshot?profileId=../x", 400}, {"/api/v1/filestation?profileId=p&snapshotId=s&nodeId=n", 503}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/api/v1/profiles" && (!strings.Contains(w.Body.String(), `"dsmBuild":"90080"`) || !strings.Contains(w.Body.String(), `"profiles":[]`)) {
			t.Fatal(w.Body.String())
		}
	}
	for _, raw := range []string{"", "buildnumber=\"90080\"\nbuildnumber=\"90081\"", "buildnumber=evil", strings.Repeat("x", 16385)} {
		if err = os.WriteFile(version, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = readDSMBuild(version); err == nil {
			t.Fatal("accepted malformed VERSION")
		}
	}
	w := httptest.NewRecorder()
	boundedReply(w, strings.Repeat("x", 64*1024))
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
}

// Known unavailable targets have a distinct safe contract; unknown IDs retain the opaque not-found response.
func TestFileStationUnavailableContract(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{jobs.ErrFileStationUnavailable, 409, "FILE_STATION_UNAVAILABLE"},
		{jobs.ErrNotFound, 404, "NOT_FOUND"},
		{jobs.ErrProfileMissing, 404, "NOT_FOUND"},
	} {
		w := httptest.NewRecorder()
		viewError(w, tc.err)
		var body struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != tc.status || body.ContractVersion != 1 || body.Error.Code != tc.code || body.Error.MessageKey != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "/volume") || strings.Contains(w.Body.String(), "path") {
			t.Fatal("unexpected diagnostic", w.Body.String())
		}
	}
}
