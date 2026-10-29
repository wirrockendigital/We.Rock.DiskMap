package service

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerExclusions keeps proposed-rule previews on the existing private, peer-checked socket.
func registerExclusions(mux *http.ServeMux, manager *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/profiles/preview-exclusions", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.ExclusionPreviewRequest
		if security.DecodeJSON(r.Body, 64*1024, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := manager.PreviewExclusions(r.Context(), in)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				err = jobs.ErrUnavailable
			}
			viewError(w, err)
			return
		}
		boundedReply(w, out)
	})
}
