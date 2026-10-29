package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// scopeAction accepts only current catalogue reads or same-origin root preflight.
func (b bridge) scopeAction(w http.ResponseWriter, r *http.Request, kind string) {
	if kind == "scope-inventory" {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		b.scopeProxy(w, r, kind, security.Principal(r.Context()), nil)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	if kind == "scope-folders" {
		var in inventory.FolderRequest
		security.MutateBounded(w, r, b.expectedOrigin, b.identity, &in, inventory.ScopeRequestLimit, func(principal, _ string) { raw, _ := json.Marshal(in); b.scopeProxy(w, r, kind, principal, raw) })
		return
	}
	var in inventory.ScopePreviewRequest
	// Reuse the read-query guard: the operation never creates a scan or changes source ACLs.
	security.MutateBounded(w, r, b.expectedOrigin, b.identity, &in, inventory.ScopeRequestLimit, func(principal, _ string) { raw, _ := json.Marshal(in); b.scopeProxy(w, r, kind, principal, raw) })
}

// scopeProxy validates a path-free typed response before the final session guard releases it.
func (b bridge) scopeProxy(w http.ResponseWriter, r *http.Request, kind, principal string, raw []byte) {
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, "http://localhost/api/v1/"+kind, bytes.NewReader(raw))
	req.Header.Set("X-DiskMap-Principal", principal)
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, e := b.client.Do(req)
	if e != nil {
		security.ActionError(w, 503, "INVENTORY_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var problem struct {
			ContractVersion int                `json:"contractVersion"`
			Error           contracts.APIError `json:"error"`
		}
		if security.DecodeJSON(response.Body, 4096, &problem) == nil && problem.ContractVersion == 1 {
			allowed := map[string]int{"INVALID_REQUEST": 400, "SCOPE_CHANGED": 409, "INVENTORY_UNAVAILABLE": 503}
			if status, ok := allowed[problem.Error.Code]; ok && status == response.StatusCode {
				security.ActionError(w, status, problem.Error.Code)
				return
			}
		}
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	var out any
	if kind == "scope-inventory" {
		var envelope contracts.Envelope[inventory.Presentation]
		if security.DecodeJSON(response.Body, inventory.PresentationLimit, &envelope) != nil || envelope.ContractVersion != 1 || !inventory.ValidPresentation(envelope.Data) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		out = envelope
	} else if kind == "scope-folders" {
		var envelope contracts.Envelope[inventory.ScopeFolders]
		if security.DecodeJSON(response.Body, inventory.PresentationLimit, &envelope) != nil || envelope.ContractVersion != 1 || !inventory.ValidFolders(envelope.Data) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		out = envelope
	} else {
		var envelope contracts.Envelope[inventory.ScopePreview]
		if security.DecodeJSON(response.Body, inventory.PresentationLimit, &envelope) != nil || envelope.ContractVersion != 1 || !inventory.ValidPreview(envelope.Data) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		out = envelope
	}
	encoded, e := json.Marshal(out)
	if e != nil || len(encoded) > inventory.PresentationLimit {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	_, _ = w.Write(encoded)
}
