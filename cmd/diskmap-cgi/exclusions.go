package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
	"reflect"
)

// Exclusion preview is a guarded read with a server-selected request budget; it never mutates profiles.
func (b bridge) exclusionPreviewAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var in jobs.ExclusionPreviewRequest
	security.MutateBounded(w, r, b.expectedOrigin, b.identity, &in, 65536, func(principal, key string) {
		raw, _ := json.Marshal(in)
		q, _ := http.NewRequestWithContext(r.Context(), "POST", "http://localhost/api/v1/profiles/preview-exclusions", bytes.NewReader(raw))
		q.Header.Set("X-DiskMap-Principal", principal)
		response, e := b.client.Do(q)
		if e != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			var failure struct {
				ContractVersion int `json:"contractVersion"`
				Error           struct {
					Code       string `json:"code"`
					MessageKey string `json:"messageKey"`
					RequestID  string `json:"requestId"`
					Retryable  bool   `json:"retryable"`
				} `json:"error"`
			}
			status := 502
			if security.DecodeJSON(response.Body, 4096, &failure) == nil && failure.ContractVersion == 1 {
				switch response.StatusCode {
				case 400:
					if failure.Error.Code == "INVALID_REQUEST" {
						status = 400
					}
				case 404:
					if failure.Error.Code == "PROFILE_NOT_FOUND" || failure.Error.Code == "NOT_FOUND" {
						status = 409
					}
				case 409:
					if failure.Error.Code == "SCAN_RESULT_UNAVAILABLE" || failure.Error.Code == "INDEX_REFRESH_REQUIRED" {
						status = 409
					}
				case 503:
					if failure.Error.Code == "SERVICE_UNAVAILABLE" || failure.Error.Code == "ACTION_BUDGET_EXCEEDED" {
						status = 503
					}
				}
			}
			security.ActionError(w, status, "PREVIEW_UNAVAILABLE")
			return
		}
		var envelope struct {
			ContractVersion int                   `json:"contractVersion"`
			RequestID       string                `json:"requestId"`
			Data            jobs.ExclusionPreview `json:"data"`
		}
		if security.DecodeJSON(response.Body, 65536, &envelope) != nil || envelope.ContractVersion != 1 || !validPreviewIntent(in, envelope.Data) || envelope.Data.SyntaxVersion != 1 || envelope.Data.Examined < 0 || envelope.Data.Examined > 1000 || envelope.Data.Matched < 0 || envelope.Data.Matched > envelope.Data.Examined || len(envelope.Data.Matches) > 50 {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		encoded, _ := json.Marshal(envelope)
		if len(encoded) > 65536 {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		w.Write(encoded)
	})
}

// Successful preview receipts bind the backend's canonical result to the exact proposed rules.
func validPreviewIntent(in jobs.ExclusionPreviewRequest, out jobs.ExclusionPreview) bool {
	effective, err := profiles.EffectiveExclusions(in.TemplateExclusions, in.Exclusions)
	if err != nil {
		var syntax *profiles.ExclusionSyntaxError
		return errors.As(err, &syntax) && !out.Valid && out.SyntaxError != nil && *out.SyntaxError == *syntax && out.SnapshotID == nil && out.Examined == 0
	}
	if !out.Valid || out.SyntaxError != nil || !reflect.DeepEqual(effective, out.EffectiveExclusions) {
		return false
	}
	if in.SyntaxOnly && (out.ProfileRevision != 0 || out.SnapshotID != nil) {
		return false
	}
	// A requested partial result cannot be substituted by the default or an absent snapshot.
	if in.PartialJobID != "" && (out.SnapshotID == nil || out.SnapshotState != "partial") {
		return false
	}
	if in.PartialJobID == "" && out.SnapshotState == "partial" {
		return false
	}
	if out.SnapshotID == nil {
		return out.SnapshotState == "none" && out.SnapshotProfileRevision == nil && out.Examined == 0 && out.Matched == 0 && len(out.Matches) == 0 && !out.Truncated && !out.MatchesTruncated
	}
	return (out.SnapshotState == "complete" || out.SnapshotState == "partial") && out.SnapshotProfileRevision != nil && *out.SnapshotProfileRevision > 0 && out.ProfileRevision > 0
}
