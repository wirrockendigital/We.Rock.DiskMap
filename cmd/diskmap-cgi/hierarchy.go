package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// hierarchyAction reuses the strict same-origin body guard for a read-only, lease-bound query.
func (b bridge) hierarchyAction(w http.ResponseWriter, r *http.Request, kind string) {
	// The shared forwarding guard preserves strict decoding for each query contract.
	var in security.ValidatedMutation = &jobs.HierarchyRequest{}
	if kind == "search" {
		in = &jobs.SearchRequest{}
	}
	security.Mutate(w, r, b.expectedOrigin, b.identity, in, func(principal, _ string) {
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/"+kind, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", principal)
		response, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			var problem struct {
				ContractVersion int `json:"contractVersion"`
				Error           struct {
					Code       string `json:"code"`
					MessageKey string `json:"messageKey"`
					RequestID  string `json:"requestId"`
					Retryable  bool   `json:"retryable"`
				} `json:"error"`
			}
			if security.DecodeJSON(response.Body, 4096, &problem) == nil && problem.ContractVersion == 1 {
				allowed := map[string]int{"INVALID_REQUEST": 400, "NOT_FOUND": 404, "VIEW_EXPIRED": 409, "SNAPSHOT_MISMATCH": 409, "QUERY_BUDGET_EXCEEDED": 503}
				if status, ok := allowed[problem.Error.Code]; ok && status == response.StatusCode {
					security.ActionError(w, status, problem.Error.Code)
					return
				}
			}
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, query.MaxResponseBytes+4097))
		if err != nil || len(body) > query.MaxResponseBytes+4096 || !json.Valid(body) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_, _ = w.Write(body)
	})
}
