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

// configurationImportMutation supplies domain validation to the unchanged shared mutation guard.
type configurationImportMutation contracts.ConfigurationImportRequest

// UnmarshalJSON preserves required false/null fields before any typed forwarding occurs.
func (in *configurationImportMutation) UnmarshalJSON(raw []byte) error {
	var value contracts.ConfigurationImportRequest
	var shape struct {
		Expected json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	apply := len(shape.Expected) > 0 && string(shape.Expected) != "null"
	if err := jobs.DecodeConfigurationImport(raw, &value, apply); err != nil {
		return err
	}
	*in = configurationImportMutation(value)
	return nil
}

func (in *configurationImportMutation) Validate() error {
	return jobs.ValidateConfigurationImport(contracts.ConfigurationImportRequest(*in), in.Expected != nil)
}

// configurationImportAction protects both preview and apply from cross-origin requests and owner substitution.
func (b bridge) configurationImportAction(w http.ResponseWriter, r *http.Request, apply bool) {
	action := "configuration-import-preview"
	if apply {
		action = "configuration-import-apply"
	}
	q := r.URL.Query()
	if len(q) != 1 || len(q["action"]) != 1 || q.Get("action") != action {
		security.ActionError(w, 400, "INVALID_REQUEST")
		return
	}
	var wire configurationImportMutation
	security.MutateConfigurationImport(w, r, b.expectedOrigin, b.identity, &wire, func(principal, key string) {
		in := contracts.ConfigurationImportRequest(wire)
		if (in.Expected != nil) != apply {
			security.ActionError(w, 400, "INVALID_REQUEST")
			return
		}
		if apply && !b.mutationsEnabled {
			security.ActionError(w, 503, "ACTION_UNAVAILABLE")
			return
		}
		raw, _ := json.Marshal(in)
		name := "configuration-import-preview"
		limit := contracts.ConfigurationPreviewLimit
		if apply {
			name = "configuration-import-apply"
			limit = 65536
		}
		request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://localhost/api/v1/"+name, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-DiskMap-Principal", principal)
		request.Header.Set("Idempotency-Key", key)
		response, e := b.client.Do(request)
		if e != nil {
			security.ActionError(w, 503, "SERVICE_UNAVAILABLE")
			return
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			configurationImportProxyError(w, response)
			return
		}
		data, e := io.ReadAll(io.LimitReader(response.Body, int64(limit)+1))
		if e != nil || len(data) > limit {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		valid := false
		if apply {
			var out contracts.Envelope[contracts.ConfigurationImportReceipt]
			valid = security.DecodeJSON(bytes.NewReader(data), int64(limit), &out) == nil && out.ContractVersion == 1 && importResponseComplete(data, out) && jobs.ValidConfigurationImportReceipt(out.Data, in)
		} else {
			var out contracts.Envelope[contracts.ConfigurationImportPreview]
			valid = security.DecodeJSON(bytes.NewReader(data), int64(limit), &out) == nil && out.ContractVersion == 1 && importResponseComplete(data, out) && jobs.ValidConfigurationImportPreview(out.Data, in)
		}
		if !valid {
			security.ActionError(w, 502, "SERVICE_RESPONSE_INVALID")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
}

// configurationImportProxyError permits fixed documented failures only, never backend diagnostic text.
func configurationImportProxyError(w http.ResponseWriter, r *http.Response) {
	var out struct {
		ContractVersion int `json:"contractVersion"`
		Error           struct {
			Code       string `json:"code"`
			MessageKey string `json:"messageKey"`
			RequestID  string `json:"requestId"`
			Retryable  bool   `json:"retryable"`
		} `json:"error"`
	}
	status, code := 502, "SERVICE_RESPONSE_INVALID"
	if security.DecodeJSON(r.Body, 4096, &out) == nil && out.ContractVersion == 1 {
		c := out.Error.Code
		if r.StatusCode == 409 && (c == "IMPORT_CONFLICT" || c == "IMPORT_UNMAPPED" || c == "IDEMPOTENCY_CONFLICT") {
			status, code = 409, c
		}
		if r.StatusCode == 400 && c == "INVALID_REQUEST" {
			status, code = 400, c
		}
		if r.StatusCode == 413 && c == "CONFIGURATION_TOO_LARGE" {
			status, code = 413, c
		}
		if r.StatusCode == 503 && (c == "SERVICE_UNAVAILABLE" || c == "ACTION_UNAVAILABLE") {
			status, code = 503, c
		}
	}
	security.ActionError(w, status, code)
}

// importResponseComplete rejects omitted false/null fields as well as otherwise valid partial responses.
func importResponseComplete(raw []byte, value any) bool {
	canonical, e := json.Marshal(value)
	if e != nil {
		return false
	}
	var a, b any
	x, y := json.NewDecoder(bytes.NewReader(raw)), json.NewDecoder(bytes.NewReader(canonical))
	x.UseNumber()
	y.UseNumber()
	return x.Decode(&a) == nil && y.Decode(&b) == nil && reflect.DeepEqual(a, b)
}
