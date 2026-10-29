package service

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerSearch exposes bounded stored-index search through the existing authenticated lease.
func registerSearch(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/search", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.SearchRequest
		principals := r.Header.Values("X-DiskMap-Principal")
		if len(principals) != 1 || security.DecodeJSON(r.Body, 32768, &in) != nil || in.Validate() != nil {
			hierarchyError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.ReadSearch(r.Context(), principals[0], in)
		if errors.Is(err, query.ErrSearch) {
			err = jobs.ErrInvalid
		}
		if err != nil {
			hierarchyError(w, err)
			return
		}
		raw, err := json.Marshal(contracts.Envelope[query.SearchPage]{ContractVersion: 1, RequestID: "search", Data: out})
		if err != nil || len(raw) > query.MaxResponseBytes+4096 {
			hierarchyError(w, query.ErrBudget)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(raw)
	})
}
