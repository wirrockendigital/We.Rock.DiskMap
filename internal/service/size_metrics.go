package service

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerSizeMetrics serves a small read-only projection behind the existing Unix peer boundary.
func registerSizeMetrics(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/size-metrics", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.SizeMetricsRequest
		p := r.Header.Values("X-DiskMap-Principal")
		if len(p) != 1 || security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
			hierarchyError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.ReadSizeMetrics(r.Context(), p[0], in)
		if err != nil {
			hierarchyError(w, err)
			return
		}
		raw, err := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "size-metrics", Data: out})
		if err != nil || len(raw) > 65536 {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
}
