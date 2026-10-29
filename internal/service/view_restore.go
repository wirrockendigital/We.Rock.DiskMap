package service

import (
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerViewRestore uses the established Unix peer boundary and authenticated principal header.
func registerViewRestore(mux *http.ServeMux, m *jobs.Manager) {
	for _, action := range []string{"restore", "navigation"} {
		navigate := action == "navigation"
		mux.HandleFunc("POST /api/v1/views/"+action, func(w http.ResponseWriter, r *http.Request) {
			principal := r.Header.Values("X-DiskMap-Principal")
			if len(principal) != 1 || r.URL.RawQuery != "" {
				hierarchyError(w, jobs.ErrInvalid)
				return
			}
			var out any
			var err error
			if navigate {
				var in jobs.NavigationRequest
				if security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
					hierarchyError(w, jobs.ErrInvalid)
					return
				}
				out, err = m.ReadNavigation(r.Context(), principal[0], in)
			} else {
				var in jobs.RestoreRequest
				if security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
					hierarchyError(w, jobs.ErrInvalid)
					return
				}
				out, err = m.RestoreView(r.Context(), principal[0], in)
			}
			if errors.Is(err, jobs.ErrPreferenceConflict) {
				security.ActionError(w, 409, "PREFERENCE_CONFLICT")
				return
			}
			if err != nil {
				hierarchyError(w, err)
				return
			}
			boundedReply(w, out)
		})
	}
}
