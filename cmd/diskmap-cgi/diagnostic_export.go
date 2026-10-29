package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// diagnosticExport forwards only the authenticated principal and a closed request shape.
func (b bridge) diagnosticExport(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(405)
		return
	}
	if len(r.URL.Query()) != 1 || len(r.URL.Query()["action"]) != 1 {
		security.ActionError(w, 400, "INVALID_REQUEST")
		return
	}
	if security.CheckMutation(r, b.expectedOrigin) != nil {
		security.ActionError(w, 403, "REQUEST_FORBIDDEN")
		return
	}
	var in service.DiagnosticExportRequest
	if security.DecodeJSON(r.Body, 4096, &in) != nil || in.ContractVersion != 1 {
		security.ActionError(w, 400, "INVALID_REQUEST")
		return
	}
	raw, _ := json.Marshal(in)
	request, _ := http.NewRequestWithContext(r.Context(), "POST", "http://localhost/api/v1/"+action, bytes.NewReader(raw))
	request.Header.Set("X-DiskMap-Principal", security.Principal(r.Context()))
	request.Header.Set("Content-Type", "application/json")
	response, e := b.client.Do(request)
	if e != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var failure struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		status, code := 502, "SERVICE_RESPONSE_INVALID"
		if security.DecodeJSON(response.Body, 4096, &failure) == nil && failure.ContractVersion == 1 {
			if response.StatusCode == 409 && failure.Error.Code == "DIAGNOSTIC_PREVIEW_MISMATCH" {
				status, code = 409, failure.Error.Code
			}
			if response.StatusCode == 503 && failure.Error.Code == "SERVICE_UNAVAILABLE" {
				status, code = 503, failure.Error.Code
			}
			if response.StatusCode == 400 && failure.Error.Code == "INVALID_REQUEST" {
				status, code = 400, failure.Error.Code
			}
		}
		security.ActionError(w, status, code)
		return
	}
	limit := diagnosticexport.MaxInputBytes + 4096
	if action == "diagnostic-export-download" {
		limit = diagnosticexport.MaxArchiveBytes
	}
	raw, e = io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	valid := e == nil && len(raw) <= limit
	if action == "diagnostic-export-preview" {
		valid = valid && service.ValidDiagnosticPreview(raw, in)
	} else {
		valid = valid && service.ValidDiagnosticArchive(raw, in.IncludePrivatePaths, in.DocumentSHA256) && (!in.IncludePrivatePaths || in.ConsentPrivatePaths)
	}
	if !valid {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	if action == "diagnostic-export-download" {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="diskmap-diagnostics.zip"`)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	_, _ = w.Write(raw)
}
