package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// profileAction shares session, origin, strict nested schema and final role recheck with scan actions.
func (b bridge) profileAction(w http.ResponseWriter, r *http.Request) {
	var in profiles.ActionRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		if !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/profile-actions", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Idempotency-Key", key)
		response, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			profileActionFailure(w, response)
			return
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || len(body) > 65536 || !json.Valid(body) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_, _ = w.Write(body)
	})
}

// profileActionFailure exposes only known product failure codes, never raw service diagnostics.
func profileActionFailure(w http.ResponseWriter, response *http.Response) {
	status, code := 502, "SERVICE_RESPONSE_INVALID"
	switch response.StatusCode {
	case 400:
		status, code = 400, "INVALID_REQUEST"
	case 404:
		status, code = 404, "PROFILE_NOT_FOUND"
	case 503:
		status, code = 503, "SERVICE_UNAVAILABLE"
		var e struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		if security.DecodeJSON(response.Body, 4096, &e) == nil && e.ContractVersion == 1 && e.Error.Code == "ACTION_BUDGET_EXCEEDED" {
			code = e.Error.Code
		}
	case 409:
		var envelope struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		if security.DecodeJSON(response.Body, 4096, &envelope) == nil && envelope.ContractVersion == 1 {
			switch envelope.Error.Code {
			case "IDEMPOTENCY_CONFLICT", "PROFILE_REVISION_CONFLICT", "PROFILE_JOB_ACTIVE", "JOB_CONFLICT", "PROFILE_REBIND_REQUIRED":
				status, code = 409, envelope.Error.Code
			}
		}
	}
	security.ActionError(w, status, code)
}
