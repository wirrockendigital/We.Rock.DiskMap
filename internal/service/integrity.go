package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerIntegrity separates bounded status reads from replay-protected explicit package-data operations.
func registerIntegrity(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/integrity", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("profileId")) {
			storageError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.IntegrityStatus(r.Context(), q.Get("profileId"))
		if err != nil {
			storageError(w, err)
			return
		}
		boundedReply(w, out)
	})
	mux.HandleFunc("POST /api/v1/integrity", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.IntegrityRequest
		p, k := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		if len(p) != 1 || len(k) != 1 || security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
			storageError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.IntegrityAction(r.Context(), in, p[0], k[0])
		if err != nil {
			storageError(w, err)
			return
		}
		boundedReply(w, out)
	})
}
