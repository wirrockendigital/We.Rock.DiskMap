package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// CLI consent is a separate argument; untrusted server text is never echoed as a diagnostic.
func TestDiagnosticExportCLI(t *testing.T) {
	in := diagnosticexport.Input{Version: 1, ObservedAt: "2026-10-11T00:00:00Z", Versions: diagnosticexport.Versions{App: "0.1.0-1", DSM: "7.4.1-90080", Architecture: "amd64", Control: 7, Generation: 11, Scanner: 1}, PrivatePaths: []diagnosticexport.PrivatePath{{Kind: "source-root", Path: "/private"}}}
	raw, _ := json.Marshal(in)
	p, _ := diagnosticexport.Project(raw, diagnosticexport.Options{IncludePrivatePaths: true})
	archive, _ := diagnosticexport.Archive(raw, diagnosticexport.Options{IncludePrivatePaths: true}, p.Digest)
	preview, _ := json.Marshal(contracts.Envelope[service.DiagnosticExportPreview]{ContractVersion: 1, Data: service.DiagnosticExportPreview{Token: strings.Repeat("a", 64), Digest: p.Digest, DocumentSHA256: fmt.Sprintf("%x", sha256.Sum256(p.Document)), ExpiresAt: "2026-10-11T01:00:00Z", IncludePrivatePaths: true, Document: p.Document, IncludedPrivatePaths: 1}})
	calls := 0
	client := &http.Client{Transport: diagnosticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("X-DiskMap-Principal") != "local-cli" {
			t.Fatal("principal")
		}
		body := preview
		content := "application/json"
		if strings.HasSuffix(r.URL.Path, "download") {
			body = archive
			content = "application/zip"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{content}}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
	var out, diag bytes.Buffer
	if code := diagnosticExportCommand(client, "diagnostic-preview", []string{"--private-paths"}, strings.NewReader(""), &out, &diag); code != 0 {
		t.Fatal(code)
	}
	req, _ := json.Marshal(service.DiagnosticExportRequest{ContractVersion: 1, IncludePrivatePaths: true, Token: strings.Repeat("a", 64), Digest: p.Digest, DocumentSHA256: fmt.Sprintf("%x", sha256.Sum256(p.Document))})
	if code := diagnosticExportCommand(client, "diagnostic-download", nil, bytes.NewReader(req), &out, &diag); code != 2 || calls != 1 {
		t.Fatal("implicit consent", code, calls)
	}
	out.Reset()
	if code := diagnosticExportCommand(client, "diagnostic-download", []string{"--consent-private-paths"}, bytes.NewReader(req), &out, &diag); code != 0 || !bytes.Equal(out.Bytes(), archive) {
		t.Fatal("archive", code)
	}
}
