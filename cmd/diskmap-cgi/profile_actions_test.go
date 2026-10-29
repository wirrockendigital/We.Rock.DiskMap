package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// TestPublicProfileMutationUsesRealDomain exercises CGI, service and SQLite together, not a success callback.
func TestPublicProfileMutationUsesRealDomain(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "original"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	p := contracts.Profile{ID: "public-probe", Revision: 1, Name: "Public probe", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if err = manager.ImportProfile(jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{"share": source}, RootKinds: map[string]string{"share": "share"}, VolumeIDs: map[string]string{"share": "volume"}}); err != nil {
		t.Fatal(err)
	}
	handler := service.ProfileActionHandler(manager)
	calls := 0
	b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "verified-admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/profile-actions" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Fatal("unsafe bridge")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Result(), nil
	})}}
	send := func(body, key string, alter func(*http.Request)) *httptest.ResponseRecorder {
		r := scanRequest(body)
		r.URL.RawQuery = "action=profile-action"
		r.Header.Set("Idempotency-Key", key)
		if alter != nil {
			alter(r)
		}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	off := `{"contractVersion":1,"operation":"deactivate","profileId":"public-probe","expectedRevision":1}`
	for _, body := range []string{strings.Replace(off, "profileId", "ProfileId", 1), strings.Replace(off, "\"expectedRevision\":1", "\"expectedRevision\":1,\"roots\":{}", 1), off + "{}"} {
		if w := send(body, "bad", nil); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := send(off, "bad", func(r *http.Request) { r.Header.Set("Origin", "https://foreign.test") }); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := send(off, "bad", func(r *http.Request) { r.Header.Del("X-SYNO-TOKEN") }); w.Code != 401 {
		t.Fatal(w.Code)
	}
	b.identity = func(context.Context) (string, error) { return "", dsmauth.ErrRole }
	if w := send(off, "bad", nil); w.Code != 403 {
		t.Fatal(w.Code)
	}
	// Lose the current administrator role during body decoding, before service invocation.
	var revoked bool
	b.identity = func(context.Context) (string, error) {
		if revoked {
			return "", dsmauth.ErrRole
		}
		return "verified-admin", nil
	}
	if w := send(off, "bad", func(r *http.Request) {
		r.Body = io.NopCloser(changingReader{reader: r.Body, change: func() { revoked = true }})
	}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if calls != 0 {
		t.Fatal("denied request caused domain mutation")
	}
	b.identity = func(context.Context) (string, error) { return "verified-admin", nil }
	first := send(off, "off", nil)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body.String())
	}
	list, err := manager.Profiles(context.Background())
	if err != nil || list[0].Profile.Active {
		t.Fatal(list, err)
	}
	repeated := send(off, "off", nil)
	if repeated.Code != 200 || repeated.Body.String() != first.Body.String() {
		t.Fatal(repeated.Code, repeated.Body.String())
	}
	if w := send(strings.Replace(off, "public-probe", "different", 1), "off", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatal(w.Code, w.Body.String())
	}
	deleted := `{"contractVersion":1,"operation":"delete","profileId":"public-probe","expectedRevision":1,"confirmOwnDataDeletion":true,"acknowledgeTasks":true,"confirmProfileName":"Public probe"}`
	if w := send(strings.Replace(deleted, `,"acknowledgeTasks":true`, "", 1), "delete", nil); w.Code != 400 {
		t.Fatal(w.Code)
	}
	accepted := send(deleted, "delete", nil)
	if accepted.Code != 200 {
		t.Fatal(accepted.Code, accepted.Body.String())
	}
	var out contracts.Envelope[jobs.ProfileActionResult]
	if json.Unmarshal(accepted.Body.Bytes(), &out) != nil || !out.Data.CleanupScheduled {
		t.Fatal(accepted.Body.String())
	}
	if w := send(deleted, "delete", nil); w.Code != 200 || w.Body.String() != accepted.Body.String() {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := send(off, "after-delete", nil); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	if raw, err := os.ReadFile(filepath.Join(source, "original")); err != nil || string(raw) != "unchanged" {
		t.Fatal("source changed", err)
	}
}
