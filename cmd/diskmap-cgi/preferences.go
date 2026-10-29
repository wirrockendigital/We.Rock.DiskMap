package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// preferenceAction obtains ownership only from the freshly authenticated DSM identity.
func (b bridge) preferenceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var in jobs.PreferenceRequest
		security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(p, k string) {
			if !b.mutationsEnabled {
				security.ActionError(w, 503, "ACTION_UNAVAILABLE")
				return
			}
			raw, _ := json.Marshal(in)
			b.preferenceProxy(w, r, p, k, raw)
		})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	b.preferenceProxy(w, r, security.Principal(r.Context()), "", nil)
}

// The outer Protect wrapper also rechecks the role before releasing read or write receipts.
func (b bridge) preferenceProxy(w http.ResponseWriter, r *http.Request, p, k string, raw []byte) {
	request, _ := http.NewRequestWithContext(r.Context(), r.Method, "http://localhost/api/v1/preferences", bytes.NewReader(raw))
	request.Header.Set("X-DiskMap-Principal", p)
	if k != "" {
		request.Header.Set("Idempotency-Key", k)
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := b.client.Do(request)
	if e != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var envelope struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		status, code := 502, "SERVICE_RESPONSE_INVALID"
		if security.DecodeJSON(response.Body, 4096, &envelope) == nil && envelope.ContractVersion == 1 {
			c := envelope.Error.Code
			if response.StatusCode == 409 && (c == "PREFERENCE_CONFLICT" || c == "IDEMPOTENCY_CONFLICT") {
				status, code = 409, c
			}
			if response.StatusCode == 400 && c == "INVALID_REQUEST" {
				status, code = 400, c
			}
			if response.StatusCode == 503 && (c == "ACTION_BUDGET_EXCEEDED" || c == "SERVICE_UNAVAILABLE") {
				status, code = 503, c
			}
		}
		security.ActionError(w, status, code)
		return
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(body) > 65536 || !validPreferenceResponse(body, p, raw) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(body)
}

// A successful private reply must match the typed versioned principal-bound DTO.
func validPreferenceResponse(body []byte, principal string, requestBody []byte) bool {
	var envelope struct {
		ContractVersion int                      `json:"contractVersion"`
		RequestID       string                   `json:"requestId"`
		Data            contracts.UserPreference `json:"data"`
	}
	if security.DecodeJSON(bytes.NewReader(body), 65536, &envelope) != nil || envelope.ContractVersion != 1 || !jobs.ValidPreference(envelope.Data, principal) {
		return false
	}
	if requestBody != nil {
		var in jobs.PreferenceRequest
		return security.DecodeJSON(bytes.NewReader(requestBody), 16384, &in) == nil && jobs.ValidPreferenceReceipt(envelope.Data, principal, in)
	}
	return true
}
