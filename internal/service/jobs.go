package service

import (
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// registerJobs exposes operations only inside the existing kernel-peer-protected API.
func registerJobs(mux *http.ServeMux, manager *jobs.Manager) {
	registerJobControl(mux, manager)
	registerProfiles(mux, manager)
	registerFileTypeMapping(mux, manager)
	mux.HandleFunc("POST /api/v1/profiles/import", func(w http.ResponseWriter, r *http.Request) {
		var in jobs.Import
		if !decodeRequest(w, r, &in) {
			return
		}
		if err := manager.ImportProfile(in); err != nil {
			jobError(w, err)
			return
		}
		reply(w, map[string]any{"profileId": in.Profile.ID, "revision": in.Profile.Revision})
	})
	mux.HandleFunc("POST /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		var in security.ScanRequest
		// All entrypoints share the same bounded, exact-case mutation schema.
		if security.DecodeJSON(r.Body, 4096, &in) != nil || in.Validate() != nil {
			jobError(w, jobs.ErrInvalid)
			return
		}
		// CGI supplies the authenticated principal; paired headers distinguish browser mutations.
		principals, keys := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
		var out jobs.Submission
		var err error
		if len(principals) == 0 && len(keys) == 0 {
			out, err = manager.Start(in.ProfileID, in.Mode)
		} else if len(principals) != 1 || len(keys) != 1 {
			err = jobs.ErrInvalid
		} else {
			out, err = manager.StartBrowser(in.ProfileID, in.Mode, jobs.BrowserStartIdentity{Principal: principals[0], Key: keys[0]})
		}
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, publicSubmission(out))
	})
	mux.HandleFunc("GET /api/v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		out, err := manager.ObserveJob(r.Context(), r.PathValue("id"))
		if err != nil {
			jobError(w, err)
			return
		}
		reply(w, publicOutcome(out))
	})
}

// decodeRequest bounds private bootstrap input and rejects ambiguous trailing data.
func decodeRequest(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		jobError(w, jobs.ErrInvalid)
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		jobError(w, jobs.ErrInvalid)
		return false
	}
	return true
}

// reply preserves the common versioned envelope for every successful local operation.
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(contracts.Envelope[any]{ContractVersion: 1, RequestID: "local", Data: v})
}

// jobError emits stable public codes rather than filesystem or SQLite diagnostics.
func jobError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "INTERNAL_ERROR"
	switch {
	case errors.Is(err, jobs.ErrActionBudget):
		status = 503
		code = "ACTION_BUDGET_EXCEEDED"
	case errors.Is(err, jobs.ErrProfileMissing):
		status = 404
		code = "PROFILE_NOT_FOUND"
	case errors.Is(err, jobs.ErrProfileConflict):
		status = 409
		code = "PROFILE_REVISION_CONFLICT"
	case errors.Is(err, jobs.ErrProfileActive):
		status = 409
		code = "PROFILE_JOB_ACTIVE"
	case errors.Is(err, jobs.ErrInvalid):
		status = 400
		code = "INVALID_REQUEST"
	case errors.Is(err, jobs.ErrIdempotencyConflict):
		status = 409
		code = "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, jobs.ErrQueueFull):
		status = 409
		code = "QUEUE_FULL"
	case errors.Is(err, jobs.ErrConflict):
		status = 409
		code = "JOB_CONFLICT"
	case errors.Is(err, jobs.ErrRootRebind):
		status = 409
		code = "PROFILE_REBIND_REQUIRED"
	case errors.Is(err, jobs.ErrUnavailable):
		status = 503
		code = "SERVICE_UNAVAILABLE"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": 1, "error": contracts.APIError{Code: code, MessageKey: code, RequestID: "local"}})
}
