package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// jobControl rechecks session identity after the strict body and before forwarding a real mutation.
func (b bridge) jobControl(w http.ResponseWriter, r *http.Request) {
	var in security.JobActionRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		if !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		body, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/job-control", bytes.NewReader(body))
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
		var envelope struct {
			ContractVersion int    `json:"contractVersion"`
			RequestID       string `json:"requestId"`
			Data            struct {
				JobID   string `json:"jobId"`
				Action  string `json:"action"`
				Version int64  `json:"version"`
			} `json:"data"`
		}
		if security.DecodeJSON(response.Body, 4096, &envelope) != nil || envelope.ContractVersion != 1 || envelope.Data.JobID != in.JobID || envelope.Data.Action != in.Action || envelope.Data.Version < 1 {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_ = json.NewEncoder(w).Encode(envelope)
	})
}
