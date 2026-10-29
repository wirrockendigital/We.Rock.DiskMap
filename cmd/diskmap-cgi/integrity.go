package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// integrityAction requires current DSM authorization, strict origin/schema and a durable replay key.
func (b bridge) integrityAction(w http.ResponseWriter, r *http.Request) {
	var in jobs.IntegrityRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		if !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/integrity", bytes.NewReader(raw))
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
			integrityFailure(w, response)
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

// integrityFailure exposes only fixed operation failures, including a full maintenance queue.
func integrityFailure(w http.ResponseWriter, response *http.Response) {
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
		allowed := map[string]int{"INVALID_REQUEST": 400, "PROFILE_NOT_FOUND": 404, "NOT_FOUND": 404, "IDEMPOTENCY_CONFLICT": 409, "PROFILE_JOB_ACTIVE": 409, "JOB_CONFLICT": 409, "QUEUE_FULL": 409, "SERVICE_UNAVAILABLE": 503, "ACTION_BUDGET_EXCEEDED": 503}
		if status, ok := allowed[envelope.Error.Code]; ok && status == response.StatusCode {
			security.ActionError(w, status, envelope.Error.Code)
			return
		}
	}
	security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
}
