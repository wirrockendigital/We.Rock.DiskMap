package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
	"io"
	"net/http"
)

// diagnosticExportCommand writes only to the explicitly selected stdout stream, never a remote destination.
// Download requests come from stdin; private archives additionally require the explicit CLI flag.
func diagnosticExportCommand(client *http.Client, command string, args []string, input io.Reader, out, diagnostic io.Writer) int {
	in := service.DiagnosticExportRequest{ContractVersion: 1}
	action := "preview"
	if command == "diagnostic-preview" {
		if len(args) > 1 || (len(args) == 1 && args[0] != "--private-paths") {
			return 2
		}
		in.IncludePrivatePaths = len(args) == 1
	} else {
		action = "download"
		if len(args) > 1 || (len(args) == 1 && args[0] != "--consent-private-paths") {
			return 2
		}
		if security.DecodeJSON(input, 4096, &in) != nil || in.ContractVersion != 1 || in.Token == "" || in.Digest == "" {
			return 2
		}
		in.ConsentPrivatePaths = len(args) == 1
		if in.IncludePrivatePaths != in.ConsentPrivatePaths {
			return 2
		}
	}
	raw, _ := json.Marshal(in)
	r, _ := http.NewRequest("POST", "http://localhost/api/v1/diagnostic-export-"+action, bytes.NewReader(raw))
	r.Header.Set("X-DiskMap-Principal", "local-cli")
	r.Header.Set("Content-Type", "application/json")
	response, e := client.Do(r)
	if e != nil {
		fmt.Fprintln(diagnostic, "SERVICE_UNAVAILABLE: service unavailable")
		return 4
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		code := 4
		switch response.StatusCode {
		case 400:
			code = 2
		case 401, 403:
			code = 3
		case 409:
			code = 5
		}
		fmt.Fprintln(diagnostic, "DIAGNOSTIC_EXPORT_UNAVAILABLE: prepare a fresh preview and check consent")
		return code
	}
	limit := 266240
	if action == "download" {
		limit = 524288
		if response.Header.Get("Content-Type") != "application/zip" {
			return 4
		}
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if e != nil || len(raw) > limit {
		return 4
	}
	if (action == "preview" && !service.ValidDiagnosticPreview(raw, in)) || (action == "download" && !service.ValidDiagnosticArchive(raw, in.IncludePrivatePaths, in.DocumentSHA256)) {
		return 4
	}
	if _, e = out.Write(raw); e != nil {
		return 4
	}
	return 0
}
