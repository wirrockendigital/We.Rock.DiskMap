package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerFileTypeMapping remains behind the existing Unix peer credentials boundary.
func registerFileTypeMapping(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/file-type-mapping", func(w http.ResponseWriter, r *http.Request) {
		out, err := m.GetFileTypeMapping(r.Context())
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
	mux.HandleFunc("PUT /api/v1/file-type-mapping", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.MappingUpdate
		if security.DecodeJSON(r.Body, 32768, &in) != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.SetFileTypeMapping(r.Context(), in)
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
}
