package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// The existing Unix peer boundary and CGI session guard protect this read-only observation.
func registerSchedulerStatus(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/scheduler-status", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("profileId")) {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.SchedulerStatus(r.Context(), q.Get("profileId"))
		if e != nil {
			jobError(w, e)
			return
		}
		boundedReply(w, out)
	})
}
