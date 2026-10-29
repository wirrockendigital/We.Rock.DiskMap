package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
)

// nodeDetailsAction checks both the incoming session and the final bounded private response.
func (b bridge) nodeDetailsAction(w http.ResponseWriter, r *http.Request) {
	var in jobs.NodeDetailsRequest
	security.Mutate(w, r, b.expectedOrigin, b.identity, &in, func(owner, _ string) {
		raw, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/node-details", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DiskMap-Principal", owner)
		resp, e := b.client.Do(req)
		if e != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			var problem struct {
				ContractVersion int                `json:"contractVersion"`
				Error           contracts.APIError `json:"error"`
			}
			allowed := map[string]int{"INVALID_REQUEST": 400, "NOT_FOUND": 404, "VIEW_EXPIRED": 409, "SNAPSHOT_MISMATCH": 409, "DETAIL_UNAVAILABLE": 409, "METRIC_UNAVAILABLE": 409, "INDEX_REFRESH_REQUIRED": 409, "QUERY_BUDGET_EXCEEDED": 503, "SERVICE_UNAVAILABLE": 503}
			if security.DecodeJSON(resp.Body, 4096, &problem) == nil && problem.ContractVersion == 1 && allowed[problem.Error.Code] == resp.StatusCode {
				// DSM replaces public CGI 404 bodies with HTML; retain a typed, non-disclosing detail error.
				if problem.Error.Code == "NOT_FOUND" {
					security.ActionError(w, http.StatusConflict, "DETAIL_UNAVAILABLE")
					return
				}
				security.ActionError(w, resp.StatusCode, problem.Error.Code)
				return
			}
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		var out contracts.Envelope[jobs.NodeDetails]
		if security.DecodeJSON(resp.Body, query.MaxDetailResponseBytes, &out) != nil || out.ContractVersion != 1 || !validNodeDetails(out.Data, in) {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		encoded, e := json.Marshal(out)
		if e != nil || len(encoded) > query.MaxDetailResponseBytes {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		_, _ = w.Write(encoded)
	})
}

// validNodeDetails binds every echoed selector; no raw private header can enter the public payload.
func validNodeDetails(out jobs.NodeDetails, in jobs.NodeDetailsRequest) bool {
	d := out.Detail
	if !query.ValidDetailResponse(d, query.DetailRequest{SnapshotID: in.SnapshotID, RootID: in.RootID, NodeID: in.NodeID, Metric: in.Metric, Operation: in.Operation, Cursor: in.Cursor, Limit: in.Limit}) {
		return false
	}
	if out.ProfileID != in.ProfileID || out.ViewID != in.ViewID || d.SnapshotID != in.SnapshotID || d.Operation != in.Operation || d.Metric != in.Metric {
		return false
	}
	if in.RootID != "nas" && d.RootID != in.RootID || in.NodeID != "nas" && d.NodeID != in.NodeID {
		return false
	}
	if d.Node != nil && (d.Node.ID != d.NodeID || d.Node.SnapshotID != d.SnapshotID) || out.Paths != nil && (out.Paths.NodeID != d.NodeID || out.Paths.SnapshotID != d.SnapshotID || out.Paths.FileStationPath != nil) {
		return false
	}
	if d.Virtual {
		return d.Node == nil && out.Paths == nil && out.PathProvenance == "not-applicable" && out.FileStationReason == "VIRTUAL_NODE"
	}
	return d.Node != nil && out.Paths != nil && out.PathProvenance == "snapshot" && out.FileStationReason == "HISTORICAL_MAPPING_NOT_RECORDED"
}
