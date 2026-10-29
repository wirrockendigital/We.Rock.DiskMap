package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// readerAction guards lease changes with the same current-session, strict-JSON and origin boundary.
func (b bridge) readerAction(w http.ResponseWriter, r *http.Request) {
	var in contracts.ViewRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/views", bytes.NewReader(raw))
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
		response, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			if response.StatusCode == 409 {
				var e struct {
					ContractVersion int `json:"contractVersion"`
					Error           struct {
						Code       string `json:"code"`
						MessageKey string `json:"messageKey"`
						RequestID  string `json:"requestId"`
						Retryable  bool   `json:"retryable"`
					} `json:"error"`
				}
				if security.DecodeJSON(response.Body, 4096, &e) == nil && e.ContractVersion == 1 && (e.Error.Code == "VIEW_EXPIRED" || e.Error.Code == "VIEW_LIMIT" || e.Error.Code == "IDEMPOTENCY_CONFLICT") {
					security.ActionError(w, 409, e.Error.Code)
					return
				}
				security.ActionError(w, 409, "SNAPSHOT_MISMATCH")
				return
			}
			security.ReadServiceFailure(w, response.StatusCode, response.Body)
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
