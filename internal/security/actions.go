package security

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"unicode/utf8"
)

// IDs and replay keys are opaque, bounded ASCII tokens, never paths or SQL fragments.
var ErrInvalidRequest = errors.New("INVALID_REQUEST")

// ValidID applies the shared public identifier grammar.
func ValidID(value string) bool {
	// The byte grammar rejects non-ASCII input without regex work for every scanned node.
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || i > 0 && (c == '_' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// TrustedOrigin validates the CGI server's HTTPS/host metadata, never forwarded headers.
func TrustedOrigin(https, authority string) string {
	if https != "on" && https != "1" {
		return ""
	}
	if strings.ContainsAny(authority, "/\\?#@ \t\r\n") || authority == "" {
		return ""
	}
	u, err := url.Parse("https://" + authority)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// CheckMutation supplements the already verified DSM token with strict browser-origin checks.
func CheckMutation(r *http.Request, expectedOrigin string) error {
	if r.Method != http.MethodPost || expectedOrigin == "" || len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != expectedOrigin {
		return ErrInvalidRequest
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		return ErrInvalidRequest
	}
	if len(r.Header.Values("X-SYNO-TOKEN")) != 1 || r.Header.Get("X-SYNO-TOKEN") == "" || len(r.Header.Get("X-SYNO-TOKEN")) > 4096 {
		return ErrInvalidRequest
	}
	if len(r.Header.Values("Idempotency-Key")) != 1 || !ValidID(r.Header.Get("Idempotency-Key")) {
		return ErrInvalidRequest
	}
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || (len(params) != 0 && (len(params) != 1 || !strings.EqualFold(params["charset"], "utf-8"))) {
		return ErrInvalidRequest
	}
	return nil
}

// DecodeJSON rejects oversized, duplicate-key, deeply nested, unknown-field and trailing input.
func DecodeJSON(body io.Reader, limit int64, target any) error {
	raw, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(raw)) > limit || !utf8.Valid(raw) {
		return ErrInvalidRequest
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueValue(d, 0); err != nil {
		return ErrInvalidRequest
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalidRequest
	}
	if !exactFields(raw, reflect.TypeOf(target)) {
		return ErrInvalidRequest
	}
	schema := json.NewDecoder(bytes.NewReader(raw))
	schema.DisallowUnknownFields()
	if err := schema.Decode(target); err != nil {
		return ErrInvalidRequest
	}
	return nil
}

// uniqueValue walks JSON tokens before schema decoding to reject ambiguous object keys.
func uniqueValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrInvalidRequest
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return ErrInvalidRequest
			}
			seen[name] = true
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueValue(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalidRequest
	}
	_, err = d.Token()
	return err
}

// ScanRequest accepts profile identities only; root mappings remain private service configuration.
type ScanRequest struct {
	ContractVersion int    `json:"contractVersion"`
	ProfileID       string `json:"profileId"`
	Mode            string `json:"mode"`
}

// Validate permits only the implemented scan mode and known public protocol.
func (s ScanRequest) Validate() error {
	if s.ContractVersion != 1 || !ValidID(s.ProfileID) || !contracts.ValidScanMode(s.Mode) {
		return ErrInvalidRequest
	}
	return nil
}

// ActionError emits fixed codes instead of upstream diagnostics, identities or input values.
func ActionError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	PrivacyHeaders(w.Header())
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": 1, "error": map[string]string{"code": code, "messageKey": code, "requestId": "action"}})
}

// exactFields enforces case-sensitive struct tags recursively before Go's permissive decoder.
func exactFields(raw []byte, t reflect.Type) bool {
	if t == nil {
		return false
	}
	// JSON null is valid for a declared nullable pointer, never for a required struct.
	if t.Kind() == reflect.Pointer && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil || object == nil {
			return false
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" || f.PkgPath != "" {
				continue
			}
			if tag == "" {
				tag = f.Name
			}
			fields[tag] = f.Type
		}
		for name, value := range object {
			field, ok := fields[name]
			if !ok || !exactFields(value, field) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return true
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, v := range values {
			if !exactFields(v, t.Elem()) {
				return false
			}
		}
	case reflect.Map:
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, v := range values {
			if !exactFields(v, t.Elem()) {
				return false
			}
		}
	}
	return true
}
