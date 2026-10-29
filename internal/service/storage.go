package service

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerStorage separates bounded observation from replay-protected global settings mutation.
func registerStorage(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("GET /api/v1/storage", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if len(q) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("profileId")) {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.Storage(r.Context(), q.Get("profileId"))
		if err != nil {
			storageError(w, err)
			return
		}
		boundedReply(w, out)
	})
	mux.HandleFunc("POST /api/v1/storage/retention", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.RetentionRequest
		p, k := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		if len(p) != 1 || len(k) != 1 || security.DecodeJSON(r.Body, 1024, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.SetRetention(r.Context(), in, p[0], k[0])
		if err != nil {
			storageError(w, err)
			return
		}
		reply(w, out)
	})
}

// Metadata deadlines are controlled availability failures, never opaque internal errors.
func storageError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		err = jobs.ErrActionBudget
	}
	jobError(w, err)
}
