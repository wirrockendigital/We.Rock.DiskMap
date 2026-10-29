package service

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// registerMapIndex exposes only qualified, bounded immutable artifact operations.
func registerMapIndex(mux *http.ServeMux, m *jobs.Manager) {
	for _, kind := range []string{"map-manifest", "map-chunks"} {
		mux.HandleFunc("POST /api/v1/"+kind, func(w http.ResponseWriter, r *http.Request) {
			var in jobs.MapRequest
			principals := r.Header.Values("X-DiskMap-Principal")
			if len(principals) != 1 || security.DecodeJSON(r.Body, 8192, &in) != nil || in.Validate() != nil {
				hierarchyError(w, jobs.ErrInvalid)
				return
			}
			var raw []byte
			var err error
			if kind == "map-manifest" {
				var out jobs.MapManifest
				out, err = m.ReadMapManifest(r.Context(), principals[0], in)
				if err == nil {
					raw, err = json.Marshal(contracts.Envelope[jobs.MapManifest]{ContractVersion: 1, RequestID: "map-manifest", Data: out})
				}
				w.Header().Set("Content-Type", "application/json")
			} else {
				raw, err = m.ReadMapChunks(r.Context(), principals[0], in)
				w.Header().Set("Content-Type", "application/octet-stream")
			}
			if err != nil {
				if errors.Is(err, jobs.ErrMapUnavailable) || errors.Is(err, jobs.ErrMapPreparing) {
					code := "MAP_INDEX_UNAVAILABLE"
					if errors.Is(err, jobs.ErrMapPreparing) {
						code = "MAP_PREPARING"
					}
					security.ActionError(w, 503, code)
				} else {
					hierarchyError(w, err)
				}
				return
			}
			if len(raw) > security.MaxGuardedResponseBytes {
				hierarchyError(w, jobs.ErrUnavailable)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(raw)
		})
	}
}
