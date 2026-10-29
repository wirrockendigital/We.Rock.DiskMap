package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// The real guarded CGI route verifies owner forwarding, typed fields, both limits and late revocation.
func TestConfigurationExportCGI(t *testing.T) {
	m, e := jobs.Open(context.Background(), t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	data, e := m.ConfigurationExport(context.Background(), "admin")
	if e != nil {
		t.Fatal(e)
	}
	data.Bundle.Profiles = []contracts.ConfigurationProfile{{SourceProfileID: "p", Configuration: contracts.ConfigurationProfileSettings{Name: "Privater Name", Scope: contracts.Scope{Kind: "nas", TargetIDs: []string{}}, Active: true, Exclusions: []contracts.Exclusion{}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}}}
	raw, _ := json.Marshal(contracts.Envelope[contracts.ConfigurationExport]{ContractVersion: 1, RequestID: "local", Data: data})
	good := string(raw)
	large := data
	large.Bundle.Profiles = []contracts.ConfigurationProfile{}
	for i := 0; i < 80; i++ {
		p := data.Bundle.Profiles[0]
		p.SourceProfileID = fmt.Sprintf("p-%03d", i)
		p.Configuration.Name = strings.Repeat("N", 1000)
		large.Bundle.Profiles = append(large.Bundle.Profiles, p)
	}
	largeRaw, _ := json.Marshal(contracts.Envelope[contracts.ConfigurationExport]{ContractVersion: 1, RequestID: "local", Data: large})
	if len(largeRaw) <= 65536 || !jobs.ValidConfigurationBundle(large.Bundle) {
		t.Fatal("invalid large fixture")
	}
	for _, tc := range []struct {
		name, body string
		revoke     bool
		want       int
	}{
		{"large-valid", string(largeRaw), false, 200}, {"large-revoked", string(largeRaw), true, 401}, {"valid", good, false, 200}, {"revoked", good, true, 401},
		{"private", strings.Replace(good, `"formatVersion":1`, `"formatVersion":1,"principal":"secret"`, 1), false, 502},
		{"missing-active", strings.Replace(good, `"active":true,`, "", 1), false, 502},
		{"missing-null", strings.Replace(good, `"locale":null,`, "", 1), false, 502},
		{"future", strings.Replace(good, `"formatVersion":1`, `"formatVersion":2`, 1), false, 502},
		{"envelope-limit", strings.Repeat(" ", contracts.ConfigurationEnvelopeLimit+1), false, 502},
		{"bundle-limit", strings.Replace(good, `"bundle":{`, `"bundle":{`+strings.Repeat(" ", contracts.ConfigurationBundleLimit), 1), false, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revoked := false
			b := bridge{identity: func(context.Context) (string, error) {
				if revoked {
					return "", dsmauth.ErrSession
				}
				return "admin", nil
			}, client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/configuration-export" || r.Header.Get("X-DiskMap-Principal") != "admin" {
					t.Fatal("unqualified request")
				}
				revoked = tc.revoke
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}}
			r := httptest.NewRequest("GET", "/?action=configuration-export", nil)
			r.Header.Set("X-SYNO-TOKEN", "test")
			w := httptest.NewRecorder()
			b.ServeHTTP(w, r)
			if tc.revoke && strings.Contains(w.Body.String(), "sourceProfileId") {
				t.Fatal("revoked data leaked")
			}
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// Admission remains read-only and rejects session/selector errors before forwarding to the private service.
func TestConfigurationExportCGIAdmission(t *testing.T) {
	for _, tc := range []struct {
		method, query, token string
		role                 error
		want                 int
	}{
		{"GET", "action=configuration-export", "", nil, 401},
		{"GET", "action=configuration-export", "test", dsmauth.ErrRole, 403},
		{"POST", "action=configuration-export", "test", nil, 405},
		{"GET", "action=configuration-export&principal=other", "test", nil, 400},
		{"GET", "action=configuration-export&action=configuration-export", "test", nil, 400},
	} {
		b := bridge{identity: func(context.Context) (string, error) { return "admin", tc.role }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil })}}
		r := httptest.NewRequest(tc.method, "/?"+tc.query, nil)
		if tc.token != "" {
			r.Header.Set("X-SYNO-TOKEN", tc.token)
		}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatal(w.Code, tc.want, w.Body.String())
		}
	}
}
