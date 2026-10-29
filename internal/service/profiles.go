package service

import (
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// registerProfiles exposes the narrow native bootstrap rename only behind the Unix peer guard.
// This route is deliberately absent from the CGI browser route allowlist.
func registerProfiles(mux *http.ServeMux, manager *jobs.Manager) {
	registerProfileActions(mux, manager)
	registerNativeRequalification(mux, manager)
	// Configuration updates remain private until the profile-actions browser contract is implemented.
	mux.HandleFunc("POST /api/v1/profiles/update", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ContractVersion  int               `json:"contractVersion"`
			ProfileID        string            `json:"profileId"`
			ExpectedRevision int               `json:"expectedRevision"`
			Profile          contracts.Profile `json:"profile"`
		}
		if !decodeRequest(w, r, &in) {
			return
		}
		if in.ContractVersion != 1 || in.ExpectedRevision < 1 || in.Profile.Revision != in.ExpectedRevision {
			jobError(w, jobs.ErrInvalid)
			return
		}
		p, err := manager.UpdateProfile(r.Context(), in.ProfileID, in.ExpectedRevision, in.Profile)
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, p)
	})

	mux.HandleFunc("POST /api/v1/profiles/rename", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ContractVersion  int    `json:"contractVersion"`
			ProfileID        string `json:"profileId"`
			ExpectedRevision int    `json:"expectedRevision"`
			Name             string `json:"name"`
		}
		if !decodeRequest(w, r, &in) {
			return
		}
		if in.ContractVersion != 1 || in.ExpectedRevision < 1 {
			jobError(w, jobs.ErrInvalid)
			return
		}
		p, err := manager.RenameProfile(r.Context(), in.ProfileID, in.ExpectedRevision, in.Name)
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, p)
	})
}
