package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
)

// registerWaitStatus is private to the existing kernel-peer-authorized Unix service.
func registerWaitStatus(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/jobs/{id}/wait-status", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.WaitStatus(r.Context(), r.PathValue("id"))
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
}
