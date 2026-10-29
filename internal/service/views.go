package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// registerViews exposes bounded read models only behind the existing Unix peer guard.
func registerViews(mux *http.ServeMux, manager *jobs.Manager, versionPath, sharesPath string) {
	registerReaders(mux, manager)
	mux.HandleFunc("GET /api/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		profiles, err := manager.Profiles(r.Context())
		if err != nil {
			viewError(w, err)
			return
		}
		build, err := readDSMBuild(versionPath)
		if err != nil {
			viewError(w, jobs.ErrUnavailable)
			return
		}
		boundedReply(w, map[string]any{"profiles": profiles, "dsmBuild": build})
	})
	mux.HandleFunc("GET /api/v1/snapshot", func(w http.ResponseWriter, r *http.Request) {
		out, err := manager.SnapshotSelection(r.Context(), r.URL.Query().Get("profileId"), r.URL.Query().Get("jobId"))
		if err != nil {
			viewError(w, err)
			return
		}
		boundedReply(w, out)
	})
	// Reports are bound to one persisted job result, including partial generations.
	mux.HandleFunc("GET /api/v1/profiles/{profileId}/scan-boundaries", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		out, err := manager.ScanBoundaries(ctx, r.PathValue("profileId"), r.URL.Query().Get("jobId"))
		if err != nil {
			viewError(w, err)
			return
		}
		boundedReply(w, out)
	})
	// Path copies expose sealed scan metadata and a separately revalidated optional share path.
	mux.HandleFunc("GET /api/v1/node-paths", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		file, err := os.Open(sharesPath)
		if err != nil {
			viewError(w, jobs.ErrUnavailable)
			return
		}
		shares, err := jobs.ParseShares(file)
		file.Close()
		if err != nil {
			viewError(w, jobs.ErrUnavailable)
			return
		}
		q := r.URL.Query()
		out, err := manager.Paths(ctx, q.Get("profileId"), q.Get("snapshotId"), q.Get("nodeId"), shares)
		if err != nil {
			viewError(w, err)
			return
		}
		boundedReply(w, out)
	})

	mux.HandleFunc("GET /api/v1/filestation", func(w http.ResponseWriter, r *http.Request) {
		// Bound registry and descriptor work independently of the caller's connection lifetime.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		file, err := os.Open(sharesPath)
		if err != nil {
			viewError(w, jobs.ErrUnavailable)
			return
		}
		shares, err := jobs.ParseShares(file)
		file.Close()
		if err != nil {
			viewError(w, err)
			return
		}
		q := r.URL.Query()
		// Only the guarded principal and its exact live lease may resolve a historical selection.
		principals := r.Header.Values("X-DiskMap-Principal")
		if len(principals) != 1 || principals[0] == "" {
			viewReaderError(w, jobs.ErrInvalid)
			return
		}
		out, err := manager.ReadLeasedFileStation(ctx, principals[0], jobs.LeasedPathsRequest{ContractVersion: 1, ProfileID: q.Get("profileId"), ViewID: q.Get("viewId"), SnapshotID: q.Get("snapshotId"), NodeID: q.Get("nodeId")}, shares)
		if err != nil {
			// Lease reads use the established bounded-query error contract.
			if errors.Is(err, jobs.ErrProfileMissing) {
				err = jobs.ErrNotFound
			}
			hierarchyError(w, err)
			return
		}
		boundedReply(w, out)
	})
}

// readDSMBuild requires one numeric buildnumber from the actual DSM VERSION file.
func readDSMBuild(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(raw) > 16384 {
		return "", jobs.ErrUnavailable
	}
	pattern := regexp.MustCompile(`^buildnumber="([0-9]+)"$`)
	found := ""
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "buildnumber=") {
			match := pattern.FindStringSubmatch(line)
			if match == nil || found != "" {
				return "", jobs.ErrUnavailable
			}
			found = match[1]
		}
	}
	if found == "" || scanner.Err() != nil {
		return "", jobs.ErrUnavailable
	}
	return found, nil
}

// boundedReply reserves space beneath the public session buffer and rejects oversized models.
func boundedReply(w http.ResponseWriter, value any) {
	raw, err := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "view", Data: value})
	if err != nil || len(raw) > 60*1024 {
		viewError(w, jobs.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// viewError never returns raw database or source filesystem errors.
func viewError(w http.ResponseWriter, err error) {
	status, code := 500, "INTERNAL_ERROR"
	switch {
	case errors.Is(err, jobs.ErrScanResultUnavailable):
		status, code = 409, "SCAN_RESULT_UNAVAILABLE"
	case errors.Is(err, jobs.ErrInvalid):
		status, code = 400, "INVALID_REQUEST"
	// A profile absent from this owner's store is an ordinary missing resource, including foreign IDs.
	case errors.Is(err, jobs.ErrNotFound), errors.Is(err, jobs.ErrProfileMissing):
		status, code = 404, "NOT_FOUND"
	case errors.Is(err, jobs.ErrFileStationUnavailable):
		status, code = 409, "FILE_STATION_UNAVAILABLE"
	case errors.Is(err, jobs.ErrRootRebind):
		status, code = 409, "PROFILE_REBIND_REQUIRED"
	case errors.Is(err, jobs.ErrGenerationRefresh):
		status, code = 409, "INDEX_REFRESH_REQUIRED"
	case errors.Is(err, jobs.ErrPathEncoding):
		status, code = 409, "PATH_ENCODING_UNSUPPORTED"
	case errors.Is(err, jobs.ErrStaleSnapshot):
		status, code = 409, "SNAPSHOT_MISMATCH"
	case errors.Is(err, jobs.ErrUnavailable):
		status, code = 503, "SERVICE_UNAVAILABLE"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": 1, "error": contracts.APIError{Code: code, MessageKey: code, RequestID: "view"}})
}
