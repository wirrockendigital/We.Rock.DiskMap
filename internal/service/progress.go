package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerProgress exposes bounded metadata-only observations behind the existing peer and CGI guards.
func registerProgress(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/progress", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q) != 1 || len(q["jobId"]) != 1 || !security.ValidID(q.Get("jobId")) {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.Progress(r.Context(), q.Get("jobId"))
		if e != nil {
			jobError(w, e)
			return
		}
		boundedReply(w, out)
	})
	mux.HandleFunc("GET /api/v1/history", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("profileId")) {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.History(r.Context(), q.Get("profileId"))
		if e != nil {
			jobError(w, e)
			return
		}
		boundedReply(w, out)
	})
}
