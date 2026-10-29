package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The public guarded route must preserve strict raw completeness before forwarding.
func TestConfigurationImportCGIBoundary(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	exported, e := m.ConfigurationExport(context.Background(), "admin")
	if e != nil {
		t.Fatal(e)
	}
	in := contracts.ConfigurationImportRequest{ContractVersion: 1, Bundle: exported.Bundle, Selection: contracts.ConfigurationSelection{ProfileIDs: []string{}, Settings: contracts.ConfigurationSettingsSelection{Appearance: true}}, Mappings: []contracts.ConfigurationTargetMapping{}}
	raw, _ := json.Marshal(in)
	good := string(raw)
	for _, tc := range []struct {
		name, body, query string
		want, calls       int
	}{
		{"valid", good, "action=configuration-import-preview", 409, 1},
		{"missing-false", strings.Replace(good, `"display":false,`, "", 1), "action=configuration-import-preview", 400, 0},
		{"missing-null", strings.Replace(good, `"expected":null`, "", 1), "action=configuration-import-preview", 400, 0},
		{"extra-query", good, "action=configuration-import-preview&principal=other", 400, 0},
		{"duplicate-query", good, "action=configuration-import-preview&action=configuration-import-preview", 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := bridge{expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				var forwarded contracts.ConfigurationImportRequest
				if jobs.DecodeConfigurationImport(body, &forwarded, false) != nil || r.Header.Get("X-DiskMap-Principal") != "admin" {
					t.Fatal("invalid forwarded input")
				}
				return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"error":{"code":"IMPORT_CONFLICT"}}`))}, nil
			})}}
			r := scanRequest(tc.body)
			r.URL.RawQuery = tc.query
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if w.Code != tc.want || calls != tc.calls {
				t.Fatal(w.Code, calls, w.Body.String())
			}
		})
	}
}

// Valid receipts pass only for the exact intent, and final authentication revocation suppresses all result data.
func TestConfigurationImportCGIReceiptAndAuth(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	exported, e := m.ConfigurationExport(context.Background(), "admin")
	if e != nil {
		t.Fatal(e)
	}
	in := contracts.ConfigurationImportRequest{ContractVersion: 1, Bundle: exported.Bundle, Selection: contracts.ConfigurationSelection{ProfileIDs: []string{}, Settings: contracts.ConfigurationSettingsSelection{Display: true}}, Mappings: []contracts.ConfigurationTargetMapping{}, Expected: &contracts.ConfigurationExpectation{MappingRevision: 1, RetentionCompleteCount: 2, CatalogDigest: strings.Repeat("a", 64)}}
	hash := func(v any) string {
		raw, _ := json.Marshal(v)
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	receipt := contracts.ConfigurationImportReceipt{BundleDigest: hash(in.Bundle), SelectionDigest: hash(struct {
		Selection contracts.ConfigurationSelection       `json:"selection"`
		Mappings  []contracts.ConfigurationTargetMapping `json:"mappings"`
	}{in.Selection, in.Mappings}), Created: []contracts.ConfigurationCreatedProfile{}, SettingsApplied: in.Selection.Settings, TasksRequireReview: true}
	response, _ := json.Marshal(contracts.Envelope[contracts.ConfigurationImportReceipt]{ContractVersion: 1, RequestID: "local", Data: receipt})
	body, _ := json.Marshal(in)
	for _, tc := range []struct {
		name, reply string
		revoke      bool
		want        int
	}{
		{"valid", string(response), false, 200}, {"revoked", string(response), true, 401}, {"missing-false", strings.Replace(string(response), `"appearance":false,`, "", 1), false, 502}, {"foreign", strings.Replace(string(response), receipt.BundleDigest, strings.Repeat("b", 64), 1), false, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revoked := false
			b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
				if revoked {
					return "", dsmauth.ErrSession
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				revoked = tc.revoke
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.reply))}, nil
			})}}
			r := scanRequest(strings.Repeat(" ", 300000) + string(body))
			r.URL.RawQuery = "action=configuration-import-apply"
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.revoke && strings.Contains(w.Body.String(), "bundleDigest") {
				t.Fatal("late owner leak")
			}
		})
	}
}
