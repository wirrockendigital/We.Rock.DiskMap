package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// profileSummariesAction reuses same-origin and final-session checks for a path-free batch read.
func (b bridge) profileSummariesAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var in jobs.ProfileSummariesRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, _ string) {
		if in.Validate() != nil {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/profile-summaries", bytes.NewReader(raw))
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Content-Type", "application/json")
		response, e := b.client.Do(req)
		if e != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			var problem struct {
				ContractVersion int                `json:"contractVersion"`
				Error           contracts.APIError `json:"error"`
			}
			allowed := map[string]int{"INVALID_REQUEST": 400, "NOT_FOUND": 404, "SERVICE_UNAVAILABLE": 503}
			if security.DecodeJSON(response.Body, 4096, &problem) == nil && problem.ContractVersion == 1 && allowed[problem.Error.Code] == response.StatusCode {
				security.ActionError(w, response.StatusCode, problem.Error.Code)
				return
			}
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		var out contracts.Envelope[jobs.ProfileSummaries]
		if security.DecodeJSON(response.Body, 65536, &out) != nil || out.ContractVersion != 1 || !jobs.ValidProfileSummaries(out.Data, in) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		encoded, e := json.Marshal(out)
		if e != nil || len(encoded) > 65536 {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_, _ = w.Write(encoded)
	})
}
