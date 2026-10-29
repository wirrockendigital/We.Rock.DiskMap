package main

import (
	"bytes"
	"encoding/json"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// onboardingAction obtains ownership only from the freshly authenticated DSM identity.
func (b bridge) onboardingAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var in jobs.OnboardingRequest
		security.MutateBounded(w, r, b.expectedOrigin, b.identity, &in, 32768, func(p, k string) {
			if !b.mutationsEnabled {
				security.ActionError(w, 503, "ACTION_UNAVAILABLE")
				return
			}
			raw, _ := json.Marshal(in)
			b.onboardingProxy(w, r, p, k, raw)
		})
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	b.onboardingProxy(w, r, security.Principal(r.Context()), "", nil)
}

// The outer Protect wrapper also rechecks the role before releasing read or write receipts.
func (b bridge) onboardingProxy(w http.ResponseWriter, r *http.Request, p, k string, raw []byte) {
	request, _ := http.NewRequestWithContext(r.Context(), r.Method, "http://localhost/api/v1/onboarding-draft", bytes.NewReader(raw))
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
			if response.StatusCode == 409 && (c == "ONBOARDING_CONFLICT" || c == "ONBOARDING_PENDING" || c == "IDEMPOTENCY_CONFLICT") {
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
	if e != nil || len(body) > 65536 || !validOnboardingResponse(body, raw) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(body)
}

// A successful private reply is both structurally valid and bound to the exact mutation intent.
func validOnboardingResponse(body, requestBody []byte) bool {
	if requestBody != nil {
		var envelope struct {
			ContractVersion int                  `json:"contractVersion"`
			RequestID       string               `json:"requestId"`
			Data            jobs.OnboardingState `json:"data"`
		}
		var in jobs.OnboardingRequest
		return security.DecodeJSON(bytes.NewReader(body), 65536, &envelope) == nil && envelope.ContractVersion == 1 && security.DecodeJSON(bytes.NewReader(requestBody), 32768, &in) == nil && jobs.ValidOnboardingReceipt(envelope.Data, in)
	}
	var envelope struct {
		ContractVersion int                     `json:"contractVersion"`
		RequestID       string                  `json:"requestId"`
		Data            jobs.OnboardingResponse `json:"data"`
	}
	return security.DecodeJSON(bytes.NewReader(body), 65536, &envelope) == nil && envelope.ContractVersion == 1 && jobs.ValidOnboardingResponse(envelope.Data)
}
