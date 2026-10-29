package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
)

// registerStatusSnapshot stays inside the existing Unix peer guard and does not mutate jobs.
func registerStatusSnapshot(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/status-snapshot", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.Status(r.Context())
		if e != nil {
			jobError(w, e)
			return
		}
		boundedReply(w, out)
	})
}
