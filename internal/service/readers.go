package service

import (
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// registerReaders accepts only the authenticated principal supplied by the guarded CGI transport.
func registerReaders(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("POST /api/v1/views", func(w http.ResponseWriter, r *http.Request) {
		var in contracts.ViewRequest
		principal := r.Header.Values("X-DiskMap-Principal")
		if len(principal) != 1 || principal[0] == "" || security.DecodeJSON(r.Body, 2048, &in) != nil || in.Validate() != nil {
			viewReaderError(w, jobs.ErrInvalid)
			return
		}
		var out any
		var err error
		switch in.Operation {
		case "open", "read":
			if in.Operation == "open" && in.SnapshotID != "" {
				out, err = m.OpenRetainedView(r.Context(), principal[0], in.ProfileID, in.ViewID, in.SnapshotID)
			} else {
				out, err = m.OpenView(r.Context(), principal[0], in.ProfileID, in.ViewID, in.JobID, in.Operation == "read")
			}
		case "release":
			if in.JobID != "" || in.NewViewID != "" || in.SnapshotID != "" || in.NodeID != "" {
				err = jobs.ErrInvalid
				break
			}
			err = m.ReleaseView(principal[0], in.ProfileID, in.ViewID)
			out = map[string]bool{"released": true}
		case "switch":
			if in.JobID != "" {
				err = jobs.ErrInvalid
				break
			}
			out, err = m.SwitchView(r.Context(), principal[0], in.ProfileID, in.ViewID, in.NewViewID, in.SnapshotID, in.NodeID)
		default:
			err = jobs.ErrInvalid
		}
		if err != nil {
			viewReaderError(w, err)
			return
		}
		boundedReply(w, out)
	})
}

// viewReaderError keeps expired identity distinct from an empty or newly selected view.
func viewReaderError(w http.ResponseWriter, err error) {
	if errors.Is(err, jobs.ErrViewExpired) {
		security.ActionError(w, 409, "VIEW_EXPIRED")
		return
	}
	if errors.Is(err, jobs.ErrIdempotencyConflict) {
		security.ActionError(w, 409, "IDEMPOTENCY_CONFLICT")
		return
	}
	if errors.Is(err, jobs.ErrViewLimit) {
		security.ActionError(w, 409, "VIEW_LIMIT")
		return
	}
	viewError(w, err)
}
