package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// configurationExport obtains its owner only from the existing guarded DSM session.
// The caller retains ProtectBounded's final role recheck before releasing the whole response.
func (b bridge) configurationExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(405)
		return
	}
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "http://localhost/api/v1/configuration-export", nil)
	request.Header.Set("X-DiskMap-Principal", security.Principal(r.Context()))
	response, e := b.client.Do(request)
	if e != nil {
		security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var envelope struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		status, code := 502, "SERVICE_RESPONSE_INVALID"
		if security.DecodeJSON(response.Body, 4096, &envelope) == nil && envelope.ContractVersion == 1 {
			c := envelope.Error.Code
			if response.StatusCode == 413 && c == "CONFIGURATION_TOO_LARGE" {
				status, code = 413, c
			}
			if response.StatusCode == 503 && c == "SERVICE_UNAVAILABLE" {
				status, code = 503, c
			}
		}
		security.ActionError(w, status, code)
		return
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, contracts.ConfigurationEnvelopeLimit+1))
	if e != nil || len(raw) > contracts.ConfigurationEnvelopeLimit || !validConfigurationExportResponse(raw) {
		security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// validConfigurationExportResponse rejects extra fields, invalid configuration and oversized bundles.
func validConfigurationExportResponse(raw []byte) bool {
	var envelope contracts.Envelope[contracts.ConfigurationExport]
	if security.DecodeJSON(bytes.NewReader(raw), contracts.ConfigurationEnvelopeLimit, &envelope) != nil || envelope.ContractVersion != 1 || len(envelope.RequestID) > 128 || !jobs.ValidConfigurationBundle(envelope.Data.Bundle) {
		return false
	}
	// Check original JSON bytes as well as canonical encoding, so whitespace cannot bypass the document limit.
	var wire struct {
		Data struct {
			Bundle json.RawMessage `json:"bundle"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &wire) != nil || len(wire.Data.Bundle) > contracts.ConfigurationBundleLimit {
		return false
	}
	// Every export field is explicit; omission/null must not silently turn into false, zero or a default.
	canonical, err := json.Marshal(envelope.Data.Bundle)
	if err != nil {
		return false
	}
	var received, expected any
	a, b := json.NewDecoder(bytes.NewReader(wire.Data.Bundle)), json.NewDecoder(bytes.NewReader(canonical))
	a.UseNumber()
	b.UseNumber()
	return a.Decode(&received) == nil && b.Decode(&expected) == nil && reflect.DeepEqual(received, expected)
}
