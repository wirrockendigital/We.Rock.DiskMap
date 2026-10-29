package service

import (
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerProfileSummaries keeps metadata reads behind the existing authorized Unix peer boundary.
func registerProfileSummaries(mux *http.ServeMux, m *jobs.Manager) {
	// Resets require a principal-bound replay key and operate solely on owned index data.
	mux.HandleFunc("POST /api/v1/index-reset", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.IndexResetRequest
		p, k := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		if len(p) != 1 || len(k) != 1 || r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 16384, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.PollIndexReset(in, p[0], k[0])
		if err != nil {
			jobError(w, err)
			return
		}
		boundedReply(w, out)
	})
	// Stable-scope metadata is independent of the active map lease and remains admin-guarded.
	mux.HandleFunc("POST /api/v1/scope-observations", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.ScopeObservationRequest
		if r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 32768, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.ScopeObservations(r.Context(), in)
		if err != nil {
			jobError(w, err)
			return
		}
		boundedReply(w, out)
	})
	mux.HandleFunc("POST /api/v1/profile-summaries", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.ProfileSummariesRequest
		if r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 16384, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.ProfileSummaries(r.Context(), in)
		if errors.Is(e, jobs.ErrNotFound) {
			security.ActionError(w, 404, "NOT_FOUND")
			return
		}
		if e != nil {
			jobError(w, e)
			return
		}
		boundedReply(w, out)
	})
}
