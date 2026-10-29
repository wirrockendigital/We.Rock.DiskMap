package main

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
	"net/http"
)

// regionalContextAction validates the bounded private reply inside the final-session guard.
func (b bridge) regionalContextAction(w http.ResponseWriter, r *http.Request) {
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://localhost/api/v1/regional-context", nil)
	response, err := b.client.Do(req)
	if err != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		security.ReadServiceFailure(w, response.StatusCode, response.Body)
		return
	}
	var out contracts.Envelope[service.RegionalContext]
	if security.DecodeJSON(response.Body, 4096, &out) != nil || out.ContractVersion != 1 || !service.ValidRegionalContext(out.Data) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 4096 {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(raw)
}
