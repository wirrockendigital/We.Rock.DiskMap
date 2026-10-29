package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// mapIndexAction buffers binary responses through the same final-session guard as JSON reads.
func (b bridge) mapIndexAction(w http.ResponseWriter, r *http.Request, kind string) {
	in := &jobs.MapRequest{}
	security.Mutate(w, r, b.expectedOrigin, b.identity, in, func(principal, _ string) {
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/"+kind, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", principal)
		resp, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			var problem struct {
				ContractVersion int `json:"contractVersion"`
				Error           struct {
					Code       string `json:"code"`
					MessageKey string `json:"messageKey"`
					RequestID  string `json:"requestId"`
					Retryable  bool   `json:"retryable"`
				} `json:"error"`
			}
			if security.DecodeJSON(resp.Body, 4096, &problem) == nil && problem.ContractVersion == 1 {
				allowed := map[string]int{"INVALID_REQUEST": 400, "NOT_FOUND": 404, "VIEW_EXPIRED": 409, "SNAPSHOT_MISMATCH": 409, "QUERY_BUDGET_EXCEEDED": 503, "MAP_PREPARING": 503, "MAP_INDEX_UNAVAILABLE": 503}
				if status, ok := allowed[problem.Error.Code]; ok && status == resp.StatusCode {
					security.ActionError(w, status, problem.Error.Code)
					return
				}
			}
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, security.MaxGuardedResponseBytes+1))
		expected := "application/json"
		if kind == "map-chunks" {
			expected = "application/octet-stream"
		}
		if err != nil || len(body) > security.MaxGuardedResponseBytes || resp.Header.Get("Content-Type") != expected || kind == "map-manifest" && !json.Valid(body) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		w.Header().Set("Content-Type", expected)
		_, _ = w.Write(body)
	})
}
