package service

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerConfigurationExport uses the existing Unix peer guard and authenticated principal transport.
func registerConfigurationExport(mux *http.ServeMux, m *jobs.Manager) {
	mux.HandleFunc("/api/v1/configuration-export", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(405)
			return
		}
		principal := r.Header.Values("X-DiskMap-Principal")
		if len(principal) != 1 || r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, e := m.ConfigurationExport(r.Context(), principal[0])
		if errors.Is(e, jobs.ErrConfigurationTooLarge) {
			security.ActionError(w, 413, "CONFIGURATION_TOO_LARGE")
			return
		}
		if e != nil {
			jobError(w, e)
			return
		}
		configurationExportReply(w, out)
	})
}

// configurationExportReply separately limits the reusable document and its API wrapper.
func configurationExportReply(w http.ResponseWriter, out contracts.ConfigurationExport) {
	raw, e := json.Marshal(out.Bundle)
	if e != nil || len(raw) > contracts.ConfigurationBundleLimit {
		security.ActionError(w, 413, "CONFIGURATION_TOO_LARGE")
		return
	}
	raw, e = json.Marshal(contracts.Envelope[contracts.ConfigurationExport]{ContractVersion: 1, RequestID: "local", Data: out})
	if e != nil || len(raw) > contracts.ConfigurationEnvelopeLimit {
		security.ActionError(w, 413, "CONFIGURATION_TOO_LARGE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}
