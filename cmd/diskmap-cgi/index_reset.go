package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// indexResetAction preserves DSM authorization and validates the durable, replayable reset receipt.
func (b bridge) indexResetAction(w http.ResponseWriter, r *http.Request) {
	var in jobs.IndexResetRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		if !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		if in.Validate() != nil {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/index-reset", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Idempotency-Key", key)
		// Each call only submits or polls the background operation within the normal CGI budget.
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
		var out contracts.Envelope[jobs.IndexResetReceipt]
		if security.DecodeJSON(response.Body, 4096, &out) != nil || out.ContractVersion != 1 || !security.ValidID(out.Data.ResetID) || out.Data.Generations < 0 {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
