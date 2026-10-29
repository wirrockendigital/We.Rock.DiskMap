package main

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The public preview preserves guards and never passes through a mismatched effective rule receipt.
func TestExclusionPreviewCGI(t *testing.T) {
	calls := 0
	revoked := false
	payload := `{"contractVersion":1,"profileId":"","syntaxOnly":true,"exclusions":[],"templateExclusions":[]}`
	good := `{"contractVersion":1,"data":{"syntaxVersion":1,"valid":true,"effectiveExclusions":[],"warnings":["SYNTAX_ONLY"],"profileRevision":0,"snapshotId":null,"snapshotProfileRevision":null,"snapshotState":"none","examined":0,"matched":0,"matches":[],"truncated":false,"matchesTruncated":false,"availablePartial":null}}`
	b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
		if revoked {
			return "", dsmauth.ErrSession
		}
		return "admin", nil
	}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(good))}, nil
	})}}
	run := func(body, origin string) int {
		r := scanRequest(body)
		r.URL.RawQuery = "action=exclusion-preview"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w.Code
	}
	if got := run(payload, "https://wrong.test"); got != 403 || calls != 0 {
		t.Fatal(got, calls)
	}
	if got := run(strings.Replace(payload, "syntaxOnly", "SyntaxOnly", 1), ""); got != 400 || calls != 0 {
		t.Fatal(got, calls)
	}
	if got := run(payload, ""); got != 200 {
		t.Fatal(got)
	}
	good = strings.Replace(good, `"effectiveExclusions":[]`, `"effectiveExclusions":[{"kind":"path","value":"foreign"}]`, 1)
	if got := run(payload, ""); got != 502 {
		t.Fatal(got)
	}
	b.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
		revoked = true
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(good))}, nil
	})
	if got := run(payload, ""); got != 401 {
		t.Fatal(got)
	}
}

// Receipt state must match the explicit partial-selection intent, never a substitute default.
func TestExclusionPreviewPartialReceiptState(t *testing.T) {
	snapshot := "snapshot"
	revision := 1
	base := jobs.ExclusionPreview{SyntaxVersion: 1, Valid: true, EffectiveExclusions: []contracts.Exclusion{}, ProfileRevision: 1, SnapshotID: &snapshot, SnapshotProfileRevision: &revision, SnapshotState: "partial"}
	request := jobs.ExclusionPreviewRequest{ContractVersion: 1, ProfileID: "profile", PartialJobID: "partial-job"}
	if !validPreviewIntent(request, base) {
		t.Fatal("matching partial rejected")
	}
	for _, state := range []string{"complete", "none"} {
		out := base
		out.SnapshotState = state
		if state == "none" {
			out.SnapshotID = nil
			out.SnapshotProfileRevision = nil
		}
		if validPreviewIntent(request, out) {
			t.Fatal("accepted substitution", state)
		}
	}
	request.PartialJobID = ""
	if validPreviewIntent(request, base) {
		t.Fatal("unsolicited partial accepted")
	}
}
