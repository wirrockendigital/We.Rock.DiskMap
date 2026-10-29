package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerJobControl retains the existing Unix-peer boundary and authenticated CGI principal.
func registerJobControl(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/job-control", func(w http.ResponseWriter, r *http.Request) {
		var in security.JobActionRequest
		principals, keys := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		if len(principals) != 1 || len(keys) != 1 || security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.ControlJob(r.Context(), in, principals[0], keys[0])
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
}

// JobControlHandler exposes the identical domain route for integration tests behind the CGI boundary.
func JobControlHandler(m *jobs.Manager) http.Handler {
	mux := http.NewServeMux()
	registerJobControl(mux, m)
	return mux
}
