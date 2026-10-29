package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Preview and archive remain owner/option/digest bound and require separate private consent.
func TestDiagnosticExportService(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	dir := t.TempDir()
	info := filepath.Join(dir, "INFO")
	version := filepath.Join(dir, "VERSION")
	os.WriteFile(info, []byte("version=\"0.1.0-1\"\n"), 0600)
	os.WriteFile(version, []byte("majorversion=\"7\"\nminorversion=\"4\"\nproductversion=\"7.4.1\"\nsmallfixnumber=\"0\"\nbuildnumber=\"90080\"\n"), 0600)
	mux := http.NewServeMux()
	registerDiagnosticExportVersion(mux, m, info, version)
	call := func(action, principal string, in DiagnosticExportRequest) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest("POST", "/api/v1/diagnostic-export-"+action, bytes.NewReader(raw))
		r.Header.Set("X-DiskMap-Principal", principal)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	for _, private := range []bool{false, true} {
		w := call("preview", "admin", DiagnosticExportRequest{ContractVersion: 1, IncludePrivatePaths: private})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var envelope contracts.Envelope[DiagnosticExportPreview]
		if json.Unmarshal(w.Body.Bytes(), &envelope) != nil {
			t.Fatal("bad preview")
		}
		p := envelope.Data
		// Qualification retains exactly the existing build-scoped DSM probe semantics.
		var document diagnosticexport.Input
		if json.Unmarshal(p.Document, &document) != nil {
			t.Fatal("document")
		}
		if document.Versions.DSM != "7.4.1-90080" || document.Versions.App != "0.1.0-1" {
			t.Fatal("native version metadata mismatch")
		}
		qualified := 0
		for _, c := range document.Capabilities {
			if c.Kind == "file-station" || c.Kind == "scheduled-scan" {
				if c.State != "qualified" {
					t.Fatal("lost build qualification")
				}
				qualified++
			}
		}
		if qualified != 2 {
			t.Fatal("missing integration observations")
		}

		req := DiagnosticExportRequest{ContractVersion: 1, IncludePrivatePaths: private, Token: p.Token, Digest: p.Digest, DocumentSHA256: p.DocumentSHA256, ConsentPrivatePaths: private}
		if call("download", "other", req).Code != 409 {
			t.Fatal("cross-owner preview")
		}
		wrong := req
		wrong.Digest = strings.Repeat("0", 64)
		if call("download", "admin", wrong).Code != 409 {
			t.Fatal("wrong digest")
		}
		if private {
			wrong = req
			wrong.ConsentPrivatePaths = false
			if call("download", "admin", wrong).Code != 409 {
				t.Fatal("private without consent")
			}
		}
		w = call("download", "admin", req)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		z, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if e != nil || len(z.File) != 2 {
			t.Fatal("invalid archive")
		}
		again := call("download", "admin", req)
		if !bytes.Equal(w.Body.Bytes(), again.Body.Bytes()) {
			t.Fatal("frozen preview changed")
		}
	}
	// No private path or decoder input may escape fixed metadata failures.
	os.WriteFile(info, []byte("version=\"secret/path\"\n"), 0600)
	if w := call("preview", "admin", DiagnosticExportRequest{ContractVersion: 1}); w.Code != 503 || strings.Contains(w.Body.String(), "secret/path") {
		t.Fatal("metadata failure")
	}
}

// Native DSM has productversion, not microversion; contradictory or malformed metadata is unavailable.
func TestDiagnosticExportNativeVersion(t *testing.T) {
	const valid = "majorversion=\"7\"\nminorversion=\"4\"\nproductversion=\"7.4.1\"\nbuildnumber=\"90080\"\nsmallfixnumber=\"0\"\n"
	path := filepath.Join(t.TempDir(), "VERSION")
	cases := map[string]string{
		"missing product":       strings.ReplaceAll(valid, "productversion=\"7.4.1\"\n", ""),
		"micro is not fallback": strings.ReplaceAll(valid, "productversion=\"7.4.1\"", "microversion=\"1\""),
		"major mismatch":        strings.ReplaceAll(valid, "majorversion=\"7\"", "majorversion=\"8\""),
		"minor mismatch":        strings.ReplaceAll(valid, "minorversion=\"4\"", "minorversion=\"3\""),
		"product suffix":        strings.ReplaceAll(valid, "7.4.1", "7.4.1-private"),
		"product incomplete":    strings.ReplaceAll(valid, "7.4.1", "7.4"),
		"build nonnumeric":      strings.ReplaceAll(valid, "90080", "private/path"),
		"missing major":         strings.ReplaceAll(valid, "majorversion=\"7\"\n", ""),
		"unbalanced quote":      strings.ReplaceAll(valid, "productversion=\"7.4.1\"", "productversion=\"7.4.1"),
		"extra quote":           strings.ReplaceAll(valid, "productversion=\"7.4.1\"", "productversion=\"\"7.4.1\"\""),
	}
	for _, key := range []string{"majorversion", "minorversion", "productversion", "buildnumber"} {
		for _, line := range strings.Split(valid, "\n") {
			if strings.HasPrefix(line, key+"=") {
				cases["duplicate "+key] = valid + line + "\n"
			}
		}
	}
	if e := os.WriteFile(path, []byte(valid), 0600); e != nil {
		t.Fatal(e)
	}
	if got, e := diagnosticVersion(path, true); e != nil || got != "7.4.1-90080" {
		t.Fatal(got, e)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
				t.Fatal(e)
			}
			if got, e := diagnosticVersion(path, true); e == nil || got != "" {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}
