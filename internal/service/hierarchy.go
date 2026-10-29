package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerHierarchy exposes only bounded stored-index reads tied to an authenticated view lease.
func registerHierarchy(mux *http.ServeMux, m *jobs.Manager) {
	registerMapIndex(mux, m)
	for _, kind := range []string{"hierarchy", "children"} {
		children := kind == "children"
		mux.HandleFunc("POST /api/v1/"+kind, func(w http.ResponseWriter, r *http.Request) {
			var in jobs.HierarchyRequest
			principals := r.Header.Values("X-DiskMap-Principal")
			if len(principals) != 1 || security.DecodeJSON(r.Body, 8192, &in) != nil || in.Validate() != nil {
				hierarchyError(w, jobs.ErrInvalid)
				return
			}
			var out any
			var err error
			out, err = m.ReadHierarchy(r.Context(), principals[0], in, children)
			if err != nil {
				hierarchyError(w, err)
				return
			}
			raw, err := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "hierarchy", Data: out})
			if err != nil || len(raw) > query.MaxResponseBytes+4096 {
				hierarchyError(w, query.ErrBudget)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(raw)
		})
	}
}

// hierarchyError exposes fixed categories and never database text or private paths.
func hierarchyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, query.ErrRequest), errors.Is(err, jobs.ErrInvalid):
		security.ActionError(w, 400, "INVALID_REQUEST")
	case errors.Is(err, query.ErrSnapshot):
		security.ActionError(w, 409, "SNAPSHOT_MISMATCH")
	case errors.Is(err, query.ErrRoot):
		security.ActionError(w, 404, "NOT_FOUND")
	case errors.Is(err, jobs.ErrFilterUnavailable):
		// Derived-cache failure does not imply damage to the stored scan generation.
		security.ActionError(w, 503, "FILTER_UNAVAILABLE")
	case errors.Is(err, query.ErrBudget), errors.Is(err, context.DeadlineExceeded):
		security.ActionError(w, 503, "QUERY_BUDGET_EXCEEDED")
	default:
		viewReaderError(w, err)
	}
}
