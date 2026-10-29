package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// The real public guard must suppress the whole archive after a late role/session revocation.
func TestDiagnosticExportCGI(t *testing.T) {
	input := diagnosticexport.Input{Version: 1, ObservedAt: "2026-10-11T00:00:00Z", Versions: diagnosticexport.Versions{App: "0.1.0-1", DSM: "7.4.1-90080", Architecture: "amd64", Control: 7, Generation: 11, Scanner: 1}, PrivatePaths: []diagnosticexport.PrivatePath{{Kind: "source-root", Path: "/secret/private"}}}
	raw, _ := json.Marshal(input)
	p, e := diagnosticexport.Project(raw, diagnosticexport.Options{})
	if e != nil {
		t.Fatal(e)
	}
	zip, e := diagnosticexport.Archive(raw, diagnosticexport.Options{}, p.Digest)
	if e != nil {
		t.Fatal(e)
	}
	preview, _ := json.Marshal(contracts.Envelope[service.DiagnosticExportPreview]{ContractVersion: 1, Data: service.DiagnosticExportPreview{Token: strings.Repeat("a", 64), Digest: p.Digest, DocumentSHA256: fmt.Sprintf("%x", sha256.Sum256(p.Document)), ExpiresAt: "2026-10-11T01:00:00Z", Document: p.Document, OmittedPrivatePaths: 1}})
	for _, action := range []string{"preview", "download"} {
		for _, revoke := range []bool{false, true} {
			revoked := false
			b := bridge{expectedOrigin: "https://nas.test", identity: func(context.Context) (string, error) {
				if revoked {
					return "", dsmauth.ErrSession
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("X-DiskMap-Principal") != "admin" {
					t.Fatal("wrong principal")
				}
				revoked = revoke
				body := preview
				if action == "download" {
					body = zip
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
			})}}
			in := service.DiagnosticExportRequest{ContractVersion: 1}
			if action == "download" {
				in.Token = strings.Repeat("a", 64)
				in.Digest = p.Digest
				in.DocumentSHA256 = fmt.Sprintf("%x", sha256.Sum256(p.Document))
			}
			body, _ := json.Marshal(in)
			req := httptest.NewRequest("POST", "/?action=diagnostic-export-"+action, bytes.NewReader(body))
			req.Header.Set("X-SYNO-TOKEN", "test")
			req.Header.Set("Origin", "https://nas.test")
			req.Header.Set("Idempotency-Key", "test")
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			b.ServeHTTP(w, req)
			want := 200
			if revoke {
				want = 401
			}
			if w.Code != want {
				t.Fatal(action, revoke, w.Code, w.Body.String())
			}
			if revoke && bytes.Contains(w.Body.Bytes(), []byte("diagnostics.json")) {
				t.Fatal("revoked archive leaked")
			}
		}
	}
	if service.ValidDiagnosticArchive(zip, false, strings.Repeat("0", 64)) {
		t.Fatal("swapped valid archive accepted")
	}
	if service.ValidDiagnosticArchive(zip, true, fmt.Sprintf("%x", sha256.Sum256(p.Document))) {
		t.Fatal("private-option mismatch accepted")
	}
	if service.ValidDiagnosticPreview(preview, service.DiagnosticExportRequest{IncludePrivatePaths: true}) {
		t.Fatal("preview option mismatch accepted")
	}
}

// Query extensions cannot bypass the action-specific response guard or reach the service.
func TestDiagnosticExportCGIQueryBoundary(t *testing.T) {
	for _, query := range []string{"action=diagnostic-export-preview&extra=1", "action=diagnostic-export-download&action=diagnostic-export-download"} {
		b := bridge{expectedOrigin: "https://nas.test", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected service call"); return nil, nil })}}
		r := httptest.NewRequest("POST", "/?"+query, strings.NewReader(`{"contractVersion":1}`))
		r.Header.Set("X-SYNO-TOKEN", "test")
		r.Header.Set("Origin", "https://nas.test")
		r.Header.Set("Idempotency-Key", "test")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
}
