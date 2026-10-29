package main

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// statusSnapshotAction validates the narrow private response under the outer final-session guard.
func (b bridge) statusSnapshotAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://localhost/api/v1/status-snapshot", nil)
	response, e := b.client.Do(req)
	if e != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		security.ReadServiceFailure(w, response.StatusCode, response.Body)
		return
	}
	var out contracts.Envelope[jobs.StatusSnapshot]
	if security.DecodeJSON(response.Body, 65536, &out) != nil || out.ContractVersion != 1 || !jobs.ValidStatusSnapshot(out.Data) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > 65536 {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(raw)
}
