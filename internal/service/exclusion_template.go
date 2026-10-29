package service

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerExclusionTemplate relies on the existing Unix peer guard, not a browser-supplied owner field.
func registerExclusionTemplate(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("/api/v1/exclusion-template", func(w http.ResponseWriter, r *http.Request) {
		p := r.Header.Values("X-DiskMap-Principal")
		if len(p) != 1 || r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		switch r.Method {
		case http.MethodGet:
			out, e := m.ExclusionTemplate(r.Context(), p[0])
			if e != nil {
				exclusionTemplateError(w, e)
				return
			}
			boundedReply(w, out)
		case http.MethodPost:
			var in jobs.ExclusionTemplateRequest
			k := r.Header.Values("Idempotency-Key")
			if len(k) != 1 || security.DecodeJSON(r.Body, 16384, &in) != nil || in.Validate() != nil {
				jobError(w, jobs.ErrInvalid)
				return
			}
			out, e := m.SetExclusionTemplate(r.Context(), in, p[0], k[0])
			if e != nil {
				exclusionTemplateError(w, e)
				return
			}
			boundedReply(w, out)
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// Errors expose only fixed codes and never stored template text or principal names.
func exclusionTemplateError(w http.ResponseWriter, e error) {
	if errors.Is(e, jobs.ErrTemplateConflict) {
		security.ActionError(w, 409, "TEMPLATE_CONFLICT")
		return
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		e = jobs.ErrActionBudget
	}
	jobError(w, e)
}
