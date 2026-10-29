package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerConfigurationImport keeps preview and atomic apply on explicit, authenticated POST boundaries.
func registerConfigurationImport(mux *http.ServeMux, m *jobs.Manager) {
	for _, apply := range []bool{false, true} {
		name := "configuration-import-preview"
		if apply {
			name = "configuration-import-apply"
		}
		mux.HandleFunc("/api/v1/"+name, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				w.WriteHeader(405)
				return
			}
			principal, key := r.Header.Values("X-DiskMap-Principal"), r.Header.Values("Idempotency-Key")
			if len(principal) != 1 || len(key) != 1 || !security.ValidID(key[0]) || r.URL.RawQuery != "" {
				jobError(w, jobs.ErrInvalid)
				return
			}
			raw, e := io.ReadAll(io.LimitReader(r.Body, contracts.ConfigurationImportLimit+1))
			var in contracts.ConfigurationImportRequest
			if e != nil {
				jobError(w, jobs.ErrInvalid)
				return
			}
			if e = jobs.DecodeConfigurationImport(raw, &in, apply); e != nil {
				configurationImportError(w, e)
				return
			}
			var out any
			if apply {
				out, e = m.ApplyConfigurationImport(r.Context(), in, principal[0], key[0])
			} else {
				out, e = m.PreviewConfigurationImport(r.Context(), in, principal[0])
			}
			if e != nil {
				configurationImportError(w, e)
				return
			}
			limit := contracts.ConfigurationPreviewLimit
			if apply {
				limit = 65536
			}
			encoded, e := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "local", Data: out})
			if e != nil || len(encoded) > limit {
				security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(encoded)
		})
	}
}

// configurationImportError never forwards private source, database or parser diagnostic text.
func configurationImportError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, jobs.ErrImportConflict):
		security.ActionError(w, 409, "IMPORT_CONFLICT")
	case errors.Is(e, jobs.ErrImportUnmapped):
		security.ActionError(w, 409, "IMPORT_UNMAPPED")
	case errors.Is(e, jobs.ErrConfigurationTooLarge):
		security.ActionError(w, 413, "CONFIGURATION_TOO_LARGE")
	default:
		jobError(w, e)
	}
}
