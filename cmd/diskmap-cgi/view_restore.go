package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net/http"
)

// viewRestoreAction requires current role and same origin before acquiring or using a view.
func (b bridge) viewRestoreAction(w http.ResponseWriter, r *http.Request, navigate bool) {
	var in security.ValidatedMutation
	action := "restore"
	if navigate {
		in = &jobs.NavigationRequest{}
		action = "navigation"
	} else {
		in = &jobs.RestoreRequest{}
	}
	security.Mutate(w, r, b.expectedOrigin, b.identity, in, func(principal, key string) {
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/views/"+action, bytes.NewReader(raw))
		req.Header.Set("X-DiskMap-Principal", principal)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", "application/json")
		response, err := b.client.Do(req)
		if err != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			var e struct {
				ContractVersion int `json:"contractVersion"`
				Error           struct {
					Code       string `json:"code"`
					MessageKey string `json:"messageKey"`
					RequestID  string `json:"requestId"`
					Retryable  bool   `json:"retryable"`
				} `json:"error"`
			}
			if security.DecodeJSON(response.Body, 4096, &e) == nil && e.ContractVersion == 1 {
				allowed := map[int]map[string]bool{400: {"INVALID_REQUEST": true}, 404: {"NOT_FOUND": true}, 409: {"VIEW_EXPIRED": true, "VIEW_LIMIT": true, "IDEMPOTENCY_CONFLICT": true, "PREFERENCE_CONFLICT": true, "SNAPSHOT_MISMATCH": true, "INDEX_REFRESH_REQUIRED": true}, 503: {"SERVICE_UNAVAILABLE": true, "QUERY_BUDGET_EXCEEDED": true}}
				if allowed[response.StatusCode][e.Error.Code] {
					security.ActionError(w, response.StatusCode, e.Error.Code)
					return
				}
			}
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || len(body) > 65536 || !validViewReply(body, in, navigate) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_, _ = w.Write(body)
	})
}

// validViewReply binds successful receipts to the exact requested lease or immutable generation.
func validViewReply(body []byte, in security.ValidatedMutation, navigate bool) bool {
	if navigate {
		var reply struct {
			ContractVersion int              `json:"contractVersion"`
			RequestID       string           `json:"requestId"`
			Data            query.Navigation `json:"data"`
		}
		request := in.(*jobs.NavigationRequest)
		return security.DecodeJSON(bytes.NewReader(body), 65536, &reply) == nil && reply.ContractVersion == 1 && reply.Data.SnapshotID == request.SnapshotID && validNavigationReply(reply.Data)
	}
	var reply struct {
		ContractVersion int                `json:"contractVersion"`
		RequestID       string             `json:"requestId"`
		Data            jobs.RestoreResult `json:"data"`
	}
	request := in.(*jobs.RestoreRequest)
	if security.DecodeJSON(bytes.NewReader(body), 65536, &reply) != nil || reply.ContractVersion != 1 || reply.Data.PreferenceRevision != request.ExpectedPreferenceRevision {
		return false
	}
	d := reply.Data
	if d.ResolvedView == nil {
		return d.Reader == nil && d.Navigation == nil
	}
	return d.ProfileID != nil && d.ResolvedView.ProfileID == *d.ProfileID && d.Reader != nil && d.Reader.Lease != nil && d.Reader.Lease.ID == request.ViewID && d.Reader.Lease.SnapshotID == d.ResolvedView.SnapshotID && d.Reader.View.SnapshotID != nil && *d.Reader.View.SnapshotID == d.ResolvedView.SnapshotID && d.Navigation != nil && d.Navigation.SnapshotID == d.ResolvedView.SnapshotID && d.Navigation.RootID == d.ResolvedView.RootID && validNavigationReply(*d.Navigation)
}

// Breadcrumbs have a hard structural and encoded budget, independent of preview truncation.
func validNavigationReply(n query.Navigation) bool {
	if len(n.Breadcrumb) < 1 || len(n.Breadcrumb) > 256 || n.Breadcrumb[len(n.Breadcrumb)-1].ID != n.RootID {
		return false
	}
	raw, err := json.Marshal(n)
	return err == nil && len(raw) <= 16384
}
