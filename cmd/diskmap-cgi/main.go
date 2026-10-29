// diskmap-cgi bridges authenticated DSM requests to the package-owned local socket.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/url"
	"os"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

type bridge struct {
	identity         func(context.Context) (string, error)
	client           *http.Client
	expectedOrigin   string
	mutationsEnabled bool
}

// ServeHTTP places every present and future public route behind the shared session guard.
func (b bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	// Only exact implemented hierarchy routes receive the larger server-defined buffer.
	limit := 64 * 1024
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodPost && (kind == "map-manifest" || kind == "map-chunks") {
		limit = security.MaxGuardedResponseBytes
	}
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodPost && (kind == "hierarchy" || kind == "children" || kind == "search") {
		limit = security.MaxGuardedResponseBytes
	}
	// Scope inventory is bounded independently from both ordinary metadata and hierarchy projections.
	if kind, err := urlQuery(r); err == nil && ((r.Method == http.MethodGet && kind == "scope-inventory") || (r.Method == http.MethodPost && (kind == "scope-preview" || kind == "scope-folders" || kind == "scope-observations"))) {
		limit = 1 << 20
	}
	// Historical detail responses have a dedicated bound below hierarchy payloads.
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodPost && kind == "node-details" {
		limit = 256 * 1024
	}
	// Regional context has a small independent response budget, including final authentication.
	if kind, err := urlQuery(r); err == nil && kind == "regional-context" {
		limit = 4096
	}
	// Only the exact read-only export route receives the bounded configuration buffer.
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodGet && kind == "configuration-export" && len(r.URL.Query()) == 1 {
		limit = contracts.ConfigurationEnvelopeLimit
	}
	// Bulk preview and diagnostic downloads have exact route-specific response budgets.
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodPost && len(r.URL.Query()) == 1 {
		switch kind {
		case "configuration-import-preview":
			limit = contracts.ConfigurationPreviewLimit
		case "diagnostic-export-preview":
			limit = 256*1024 + 4096
		case "diagnostic-export-download":
			limit = 512 * 1024
		}
	}
	// The exact authenticated scan-preview GET uses the same envelope limit as its backend reader.
	if kind, err := urlQuery(r); err == nil && r.Method == http.MethodGet && kind == "scan-preview" {
		limit = 128 * 1024
	}
	security.ProtectBounded(b.identity, http.HandlerFunc(b.route), limit).ServeHTTP(w, r)
}

// route exposes only implemented operations, without arbitrary backend paths or file access.
func (b bridge) route(w http.ResponseWriter, r *http.Request) {
	query, err := urlQuery(r)
	if err != nil {
		security.ActionError(w, 400, "INVALID_REQUEST")
		return
	}
	if r.Method == http.MethodPost && (query == "map-manifest" || query == "map-chunks") {
		b.mapIndexAction(w, r, query)
		return
	}
	// These feature owners enforce method, origin, strict shape and current session themselves.
	if query == "configuration-import-preview" || query == "configuration-import-apply" {
		b.configurationImportAction(w, r, query == "configuration-import-apply")
		return
	}
	if query == "diagnostic-export-preview" || query == "diagnostic-export-download" {
		b.diagnosticExport(w, r, query)
		return
	}
	if query == "scope-inventory" || query == "scope-preview" || query == "scope-folders" {
		b.scopeAction(w, r, query)
		return
	}
	if query == "preferences" {
		b.preferenceAction(w, r)
		return
	}
	// The shared template provider applies its own method and revision checks.
	if query == "exclusion-template" {
		b.exclusionTemplateAction(w, r)
		return
	}
	// Proposed exclusions use the same authenticated boundary and a bounded read-only preview.
	if query == "exclusion-preview" {
		b.exclusionPreviewAction(w, r)
		return
	}
	// Principal-bound onboarding drafts retain their own bounded mutation contract.
	if query == "onboarding-draft" {
		b.onboardingAction(w, r)
		return
	}
	// Reopening and navigation never accept a client-selected preference owner.
	if query == "view-restore" || query == "view-navigation" {
		b.viewRestoreAction(w, r, query == "view-navigation")
		return
	}
	if r.Method == http.MethodPost && (query == "hierarchy" || query == "children" || query == "search") {
		b.hierarchyAction(w, r, query)
		return
	}
	// Exact sizes and independent capacity metadata require the same authenticated view lease.
	if r.Method == http.MethodPost && query == "size-metrics" {
		b.sizeMetricsAction(w, r)
		return
	}
	// Full details read only the qualified historical generation.
	if r.Method == http.MethodPost && query == "node-details" {
		b.nodeDetailsAction(w, r)
		return
	}
	// Captured path details use a lease without probing the current source.
	if r.Method == http.MethodPost && query == "leased-paths" {
		b.leasedPathsAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "job-control" {
		b.jobControl(w, r)
		return
	}
	// Integrity mutations retain the same current-role and origin checks as scans.
	if r.Method == http.MethodPost && query == "integrity-action" {
		b.integrityAction(w, r)
		return
	}
	// Retention changes use the same final role and replay guard as other mutations.
	if r.Method == http.MethodPost && query == "storage-retention" {
		b.storageAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "view" {
		b.readerAction(w, r)
		return
	}
	// Profile summaries share the authenticated, bounded request boundary.
	if r.Method == http.MethodPost && query == "index-reset" {
		b.indexResetAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "scope-observations" {
		b.scopeObservationsAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "profile-summaries" {
		b.profileSummariesAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "profile-action" {
		b.profileAction(w, r)
		return
	}
	if r.Method == http.MethodPost && query == "scan" {
		b.scan(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	backendPath := "/api/v1/health"
	switch query {
	case "session":
		_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": 1, "requestId": "session", "data": map[string]any{"authenticated": true, "principal": security.Principal(r.Context())}})
		return
	case "regional-context":
		b.regionalContextAction(w, r)
		return
	case "configuration-export":
		// Client-selected scope or principal parameters cannot influence an export.
		if len(r.URL.Query()) != 1 {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		b.configurationExport(w, r)
		return
	case "status-snapshot":
		// The global status contract accepts no client-selected job or profile scope.
		if len(r.URL.Query()) != 1 {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		b.statusSnapshotAction(w, r)
		return
	case "scheduler-status":
		backendPath = "/api/v1/scheduler-status?" + url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}.Encode()
	case "scheduler-template":
		backendPath = "/api/v1/profiles/" + r.URL.Query().Get("profileId") + "/scheduler-template"
	case "diagnostics":
		backendPath = "/api/v1/diagnostics"
	case "health":
	case "storage":
		backendPath = "/api/v1/storage?" + url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}.Encode()
	case "scan-preview":
		backendPath = "/api/v1/scan-preview?" + url.Values{"jobId": {r.URL.Query().Get("jobId")}, "profileId": {r.URL.Query().Get("profileId")}}.Encode()
	case "progress":
		// Only a validated job identifier crosses the authenticated read boundary.
		backendPath = "/api/v1/progress?" + url.Values{"jobId": []string{r.URL.Query().Get("jobId")}}.Encode()
	case "history":
		backendPath = "/api/v1/history?" + url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}.Encode()
	case "integrity-status":
		backendPath = "/api/v1/integrity?" + url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}.Encode()
	case "profiles":
		backendPath = "/api/v1/profiles"
	case "job":
		backendPath = "/api/v1/jobs/" + r.URL.Query().Get("id")
	case "profile-cleanup":
		backendPath = "/api/v1/profile-cleanup?" + url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}.Encode()
	case "scan-boundaries":
		backendPath = "/api/v1/profiles/" + r.URL.Query().Get("profileId") + "/scan-boundaries?" + url.Values{"jobId": []string{r.URL.Query().Get("jobId")}}.Encode()
	case "snapshot":
		query := url.Values{"profileId": []string{r.URL.Query().Get("profileId")}}
		if r.URL.Query().Has("jobId") {
			query.Set("jobId", r.URL.Query().Get("jobId"))
		}
		backendPath = "/api/v1/snapshot?" + query.Encode()
	case "filestation", "node-paths":
		parameters := r.URL.Query()
		parameters.Del("action")
		backendPath = "/api/v1/" + query + "?" + parameters.Encode()
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://localhost"+backendPath, nil)
	// File Station is bound to the final authenticated principal, never a client-supplied header.
	if query == "filestation" || query == "scan-preview" {
		request.Header.Set("X-DiskMap-Principal", security.Principal(r.Context()))
	}
	response, err := b.client.Do(request)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		security.ReadServiceFailure(w, response.StatusCode, response.Body)
		return
	}
	limit := int64(64 * 1024)
	if query == "scan-preview" {
		limit = 128 * 1024
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(body)) > limit || !json.Valid(body) {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

// urlQuery accepts one bounded action selector and rejects ambiguous query parameters.
func urlQuery(r *http.Request) (string, error) {
	if len(r.URL.RawQuery) > 1024 {
		return "", security.ErrInvalidRequest
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q["action"]) != 1 {
		return "", security.ErrInvalidRequest
	}
	fields := map[string][]string{"regional-context": {}, "status-snapshot": {}, "view-restore": {}, "view-navigation": {}, "scope-inventory": {}, "scope-preview": {}, "scope-folders": {}, "preferences": {}, "exclusion-template": {}, "exclusion-preview": {}, "onboarding-draft": {}, "leased-paths": {}, "node-details": {}, "size-metrics": {}, "search": {}, "scheduler-template": {"profileId"}, "scheduler-status": {"profileId"}, "diagnostics": {}, "integrity-action": {}, "integrity-status": {"profileId"}, "scan-preview": {"jobId", "profileId"}, "progress": {"jobId"}, "history": {"profileId"}, "hierarchy": {}, "map-manifest": {}, "map-chunks": {}, "children": {}, "storage": {"profileId"}, "storage-retention": {}, "job-control": {}, "view": {}, "session": {}, "health": {}, "profiles": {}, "profile-action": {}, "profile-summaries": {}, "scope-observations": {}, "index-reset": {}, "profile-cleanup": {"profileId"}, "scan": {}, "job": {"id"}, "scan-boundaries": {"profileId", "jobId"}, "snapshot": {"profileId"}, "filestation": {"profileId", "snapshotId", "nodeId", "viewId"}, "node-paths": {"profileId", "snapshotId", "nodeId"}}
	allowed, known := fields[q.Get("action")]
	if !known {
		if len(q) != 1 {
			return "", security.ErrInvalidRequest
		}
		return q.Get("action"), nil
	}
	// An explicit partial result is optional; all supplied fields retain strict validation.
	if q.Get("action") == "snapshot" && q.Has("jobId") {
		allowed = append(append([]string{}, allowed...), "jobId")
	}
	if len(q) != len(allowed)+1 {
		return "", security.ErrInvalidRequest
	}
	for _, key := range allowed {
		if len(q[key]) != 1 || !security.ValidID(q.Get(key)) {
			return "", security.ErrInvalidRequest
		}
	}
	return q.Get("action"), nil
}

// scan forwards only validated profile requests once durable service replay support is enabled.
func (b bridge) scan(w http.ResponseWriter, r *http.Request) {
	var input security.ScanRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &input, func(principal, key string) { b.submitScan(w, r, input, principal, key) })
}

// submitScan invokes the real service only after the shared mutation guard authorizes it.
func (b bridge) submitScan(w http.ResponseWriter, r *http.Request, input security.ScanRequest, principal, key string) {
	if !b.mutationsEnabled {
		security.ActionError(w, 503, "ACTION_UNAVAILABLE")
		return
	}
	body, _ := json.Marshal(input)
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/jobs", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-DiskMap-Principal", principal)
	response, err := b.client.Do(request)
	if err != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		status, code := 502, "SERVICE_RESPONSE_INVALID"
		switch response.StatusCode {
		case 400:
			status, code = 400, "INVALID_REQUEST"
		case 403:
			status, code = 403, "REQUEST_FORBIDDEN"
		case 409:
			var envelope struct {
				ContractVersion int `json:"contractVersion"`
				Error           struct {
					Code       string `json:"code"`
					MessageKey string `json:"messageKey"`
					RequestID  string `json:"requestId"`
					Retryable  bool   `json:"retryable"`
				} `json:"error"`
			}
			if security.DecodeJSON(response.Body, 4096, &envelope) == nil && envelope.ContractVersion == 1 && (envelope.Error.Code == "IDEMPOTENCY_CONFLICT" || envelope.Error.Code == "JOB_CONFLICT" || envelope.Error.Code == "QUEUE_FULL") {
				status, code = 409, envelope.Error.Code
			}
		case 503:
			status, code = 503, "SERVICE_UNAVAILABLE"
		}
		security.ActionError(w, status, code)
		return
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(result) > 64*1024 || !json.Valid(result) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(result)
}

func main() {
	// CGI uses the DSM server-provided request environment without a second listener.
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/packages/we-rock-diskmap/var/run/control.sock")
	}}
	defer transport.CloseIdleConnections()
	// Scan mutations use the integrated transactional job/replay binding in the local service.

	_ = cgi.Serve(bridge{identity: dsmauth.Identity, mutationsEnabled: true, expectedOrigin: security.TrustedOrigin(os.Getenv("HTTPS"), os.Getenv("HTTP_HOST")), client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}})
}
