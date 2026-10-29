package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// storageAction shares session, origin, strict nested schema and final role recheck with scan actions.
func (b bridge) storageAction(w http.ResponseWriter, r *http.Request) {
	var in jobs.RetentionRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, key string) {
		if !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/storage/retention", bytes.NewReader(raw))
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
