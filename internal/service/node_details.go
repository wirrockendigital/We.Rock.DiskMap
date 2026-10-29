package service

import (
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerNodeDetails exposes bounded immutable reads behind kernel-peer authorization.
func registerNodeDetails(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/node-details", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.NodeDetailsRequest
		p := r.Header.Values("X-DiskMap-Principal")
		if len(p) != 1 || r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 8192, &in) != nil || in.Validate() != nil {
			hierarchyError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.ReadNodeDetails(r.Context(), p[0], in)
		if errors.Is(e, query.ErrDetailUnavailable) {
			security.ActionError(w, 409, "DETAIL_UNAVAILABLE")
			return
		}
		if errors.Is(e, query.ErrDetailMetric) {
			security.ActionError(w, 409, "METRIC_UNAVAILABLE")
			return
		}
		if errors.Is(e, query.ErrSearch) {
			hierarchyError(w, jobs.ErrInvalid)
			return
		}
		if e != nil {
			hierarchyError(w, e)
			return
		}
		raw, e := json.Marshal(contracts.Envelope[jobs.NodeDetails]{ContractVersion: 1, RequestID: "node-details", Data: out})
		if e != nil || len(raw) > query.MaxDetailResponseBytes {
			hierarchyError(w, query.ErrBudget)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
}
