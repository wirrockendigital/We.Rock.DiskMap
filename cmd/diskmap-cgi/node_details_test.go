package main

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Real dispatch accepts a bounded typed historical result and rejects mismatched or revoked responses.
func TestNodeDetailsCGIBinding(t *testing.T) {
	sum := query.Summary{KnownBytes: "0", UnknownFileCount: "0", FileCount: "1", DirectoryCount: "0", SpecialCount: "0"}
	zero := contracts.Bytes("0")
	data := jobs.NodeDetails{ProfileID: "p", ViewID: "v", Detail: query.DetailResponse{SnapshotID: "s", RootID: "root", NodeID: "n", Operation: "details", Metric: "logical", Kind: "file", Name: "saved", Node: &contracts.Node{SnapshotID: "s", ID: "n", Kind: "file", LogicalBytes: &zero}, Numerator: sum, Denominator: sum, Basis: "unfiltered-recorded-path-entries", ProfileRevision: 1, SnapshotState: "complete", BoundaryCount: "0", RankingSemantics: "not-requested"}, Paths: &jobs.NodePaths{SnapshotID: "s", NodeID: "n", Name: jobs.PathText{Text: "saved", Encoding: "utf8"}, NASPath: jobs.PathText{Text: "/recorded/saved", Encoding: "utf8"}}, PathProvenance: "snapshot", FileStationReason: "HISTORICAL_MAPPING_NOT_RECORDED"}
	// The mandatory historical-availability DTO uses an explicit empty observation.
	data.Detail.SourceAvailability = query.SourceAvailability{Total: "0", Records: []query.SourceAvailabilityRecord{}}
	raw, e := json.Marshal(contracts.Envelope[jobs.NodeDetails]{ContractVersion: 1, Data: data})
	if e != nil {
		t.Fatal(e)
	}
	good := string(raw)
	for _, tc := range []struct {
		body    string
		revoked bool
		want    int
	}{{good, false, 200}, {strings.Replace(good, `"rootId":"root"`, `"rootId":"wrong"`, 1), false, 502}, {good, true, 401}, {strings.Repeat(" ", query.MaxDetailResponseBytes+1), false, 502}} {
		revoked := false
		b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
			if revoked {
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/node-details" || r.Header.Get("X-DiskMap-Principal") != "admin" {
				t.Fatal("wrong bridge")
			}
			revoked = tc.revoked
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		r := scanRequest(`{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","rootId":"root","nodeId":"n","metric":"logical","operation":"details"}`)
		r.URL.RawQuery = "action=node-details"
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

// Only a validated private not-found result becomes a typed public detail-unavailable result.
func TestNodeDetailsCGINotFoundRemainsJSON(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
		code   string
	}{
		{"qualified", 404, `{"contractVersion":1,"error":{"code":"NOT_FOUND"}}`, 409, "DETAIL_UNAVAILABLE"},
		{"wrong-version", 404, `{"contractVersion":2,"error":{"code":"NOT_FOUND"}}`, 502, "SERVICE_RESPONSE_INVALID"},
		{"wrong-status", 500, `{"contractVersion":1,"error":{"code":"NOT_FOUND"}}`, 502, "SERVICE_RESPONSE_INVALID"},
		{"html", 404, `<html>unavailable</html>`, 502, "SERVICE_RESPONSE_INVALID"},
		{"snapshot", 409, `{"contractVersion":1,"error":{"code":"SNAPSHOT_MISMATCH"}}`, 409, "SNAPSHOT_MISMATCH"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			r := scanRequest(`{"contractVersion":1,"profileId":"p","viewId":"v","snapshotId":"s","rootId":"root","nodeId":"n","metric":"logical","operation":"details"}`)
			r.URL.RawQuery = "action=node-details"
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			var out struct {
				ContractVersion int                `json:"contractVersion"`
				Error           contracts.APIError `json:"error"`
			}
			if w.Code != tc.want || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.ContractVersion != 1 || out.Error.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
