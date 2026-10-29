package service

import (
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerNativeRequalification exposes explicit recovery only through the private Unix peer boundary.
// No CGI route accepts this operation and callers cannot supply paths or replacement identities.
func registerNativeRequalification(mux *http.ServeMux, manager *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/profiles/requalify-native", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ContractVersion  int    `json:"contractVersion"`
			ProfileID        string `json:"profileId"`
			ExpectedRevision int    `json:"expectedRevision"`
		}
		if r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 2048, &in) != nil || in.ContractVersion != 1 || !security.ValidID(in.ProfileID) || in.ExpectedRevision < 1 {
			jobError(w, jobs.ErrInvalid)
			return
		}
		profile, err := manager.RequalifyNativeProfile(r.Context(), in.ProfileID, in.ExpectedRevision)
		if err != nil {
			jobError(w, err)
			return
		}
		boundedReply(w, profile)
	})
}
