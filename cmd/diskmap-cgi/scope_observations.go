package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// scopeObservationsAction admits only validated share-relative metadata requests through the DSM gate.
func (b bridge) scopeObservationsAction(w http.ResponseWriter, r *http.Request) {
	var in jobs.ScopeObservationRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(principal, _ string) {
		if in.Validate() != nil {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/scope-observations", bytes.NewReader(raw))
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Content-Type", "application/json")
		res, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer res.Body.Close()
		var out contracts.Envelope[[]jobs.ScopeObservation]
		if res.StatusCode != 200 || security.DecodeJSON(res.Body, 1024*1024, &out) != nil || out.ContractVersion != 1 || len(out.Data) != len(in.Scopes) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		for i, v := range out.Data {
			if (jobs.ObservedScope{ShareID: v.ShareID, Path: v.Path}) != in.Scopes[i] || (v.Bytes != nil && v.Bytes.Validate() != nil) {
				security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
				return
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
