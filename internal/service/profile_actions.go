package service

import (
	"net/http"
	"net/url"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerProfileActions requires the CGI's paired authenticated-principal/replay headers.
// The existing Unix peer guard remains mandatory for this private transport.
func registerProfileActions(mux *http.ServeMux, manager *jobs.Manager) {
	mux.Handle("POST /api/v1/profile-actions", ProfileActionHandler(manager))
	mux.HandleFunc("GET /api/v1/profile-cleanup", func(w http.ResponseWriter, r *http.Request) {
		q, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(r.URL.RawQuery) > 256 || len(q) != 1 || len(q["profileId"]) != 1 || !security.ValidID(q.Get("profileId")) {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := manager.ProfileCleanupStatus(r.Context(), q.Get("profileId"))
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
}

// ProfileActionHandler implements the production action transport behind the Unix peer guard.
func ProfileActionHandler(manager *jobs.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in profiles.ActionRequest
		principal, key := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		if len(principal) != 1 || len(key) != 1 || security.DecodeJSON(r.Body, 16384, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := manager.ProfileAction(r.Context(), in, principal[0], key[0])
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, out)
	})
}
