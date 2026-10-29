package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// TestPrivateRenameBoundary validates exact IDs and revisions without exposing private mappings.
func TestPrivateRenameBoundary(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	m, err := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	p := contracts.Profile{ID: "probe", Revision: 1, Name: "Old", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if err = m.ImportProfile(jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume"}, RootKinds: map[string]string{"share": "share"}}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerJobs(mux, m)
	for _, tc := range []struct {
		payload string
		status  int
	}{
		{`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"name":"Neu – 東京"}`, 200},
		{`{"contractVersion":1,"profileId":"probe","expectedRevision":2,"name":"Bad"}`, 409},
		{`{"contractVersion":1,"profileId":"missing","expectedRevision":1,"name":"Bad"}`, 400},
		{`{"contractVersion":1,"profileId":"probe","expectedRevision":0,"name":"Bad"}`, 400},
		{`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"name":""}`, 400},
		{`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"name":"x","path":"/arbitrary"}`, 400},
	} {
		r := httptest.NewRequest("POST", "/api/v1/profiles/rename", strings.NewReader(tc.payload))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), source) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// TestPrivateUpdateRejectsAmbiguousRevision prevents DTO and target-identity substitution.
func TestPrivateUpdateRejectsAmbiguousRevision(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	mux := http.NewServeMux()
	registerJobs(mux, m)
	for _, payload := range []string{
		`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"profile":{"id":"probe","revision":2}}`,
		`{"contractVersion":2,"profileId":"probe","expectedRevision":1,"profile":{"id":"probe","revision":1}}`,
		`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"profile":{"id":"other","revision":1}}`,
		`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"profile":{"id":"probe","revision":1},"roots":{}}`,
	} {
		r := httptest.NewRequest("POST", "/api/v1/profiles/update", strings.NewReader(payload))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
