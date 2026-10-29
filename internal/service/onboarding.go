package service

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerOnboarding relies on the existing Unix peer guard, not a browser-supplied owner field.
func registerOnboarding(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("/api/v1/onboarding-draft", func(w http.ResponseWriter, r *http.Request) {
		p := r.Header.Values("X-DiskMap-Principal")
		if len(p) != 1 || r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		switch r.Method {
		case http.MethodGet:
			out, e := m.OnboardingDraft(r.Context(), p[0])
			if e != nil {
				onboardingError(w, e)
				return
			}
			boundedReply(w, out)
		case http.MethodPost:
			var in jobs.OnboardingRequest
			k := r.Header.Values("Idempotency-Key")
			if len(k) != 1 || security.DecodeJSON(r.Body, 32768, &in) != nil || in.Validate() != nil {
				jobError(w, jobs.ErrInvalid)
				return
			}
			out, e := m.SetOnboardingDraft(r.Context(), in, p[0], k[0])
			if e != nil {
				onboardingError(w, e)
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
func onboardingError(w http.ResponseWriter, e error) {
	if errors.Is(e, jobs.ErrOnboardingPending) {
		security.ActionError(w, 409, "ONBOARDING_PENDING")
		return
	}
	if errors.Is(e, jobs.ErrOnboardingConflict) {
		security.ActionError(w, 409, "ONBOARDING_CONFLICT")
		return
	}
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		e = jobs.ErrActionBudget
	}
	jobError(w, e)
}
