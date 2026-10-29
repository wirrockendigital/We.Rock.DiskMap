package service

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerScanPreview shares the authenticated CGI boundary and returns no actionable node IDs.
func registerScanPreview(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/scan-preview", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		principals := r.Header.Values("X-DiskMap-Principal")
		if len(q) != 2 || len(q["jobId"]) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("jobId")) || !security.ValidID(q.Get("profileId")) || len(principals) != 1 {
			jobError(w, jobs.ErrInvalid)
			return
		}
		value, err := m.ReadScanPreview(r.Context(), principals[0], q.Get("jobId"), q.Get("profileId"))
		if err != nil {
			jobError(w, err)
			return
		}
		// Preview has its own explicit byte budget, independent of normal view replies.
		raw, err := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "scan-preview", Data: value})
		if err != nil || len(raw) > 128*1024 {
			jobError(w, jobs.ErrUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
}
