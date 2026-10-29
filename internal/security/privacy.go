package security

import (
	"io"
	"net/http"
)

// PrivacyHeaders applies to every authenticated, denied, and malformed public response.
// No browser or shared proxy may persist application metadata; tokens never become cache keys in URLs.
func PrivacyHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Vary", "Cookie, X-SYNO-TOKEN")
}

// ReadServiceFailure retains only fixed public codes; private diagnostics are discarded wholesale.
// This is intentionally not a heuristic redactor that might miss a name inside nested error text.
func ReadServiceFailure(w http.ResponseWriter, status int, body io.Reader) {
	code := "SERVICE_RESPONSE_INVALID"
	switch status {
	case 400:
		code = "INVALID_REQUEST"
	case 401:
		code = "SESSION_REQUIRED"
	case 403:
		code = "REQUEST_FORBIDDEN"
	case 404:
		code = "NOT_FOUND"
	case 500:
		code = "INTERNAL_ERROR"
	case 503:
		code = "SERVICE_UNAVAILABLE"
	case 409:
		var envelope struct {
			ContractVersion int `json:"contractVersion"`
			Error           struct {
				Code       string `json:"code"`
				MessageKey string `json:"messageKey"`
				RequestID  string `json:"requestId"`
				Retryable  bool   `json:"retryable"`
			} `json:"error"`
		}
		if DecodeJSON(body, 4096, &envelope) == nil && envelope.ContractVersion == 1 {
			switch envelope.Error.Code {
			// Leased read actions must retain expiry without forwarding any private diagnostic fields.
			case "VIEW_EXPIRED", "FILE_STATION_UNAVAILABLE", "PROFILE_REBIND_REQUIRED", "INDEX_REFRESH_REQUIRED", "PATH_ENCODING_UNSUPPORTED", "SNAPSHOT_MISMATCH", "SCAN_RESULT_UNAVAILABLE":
				code = envelope.Error.Code
			}
		}
		if code == "SERVICE_RESPONSE_INVALID" {
			status = 502
		}
	default:
		status = 502
	}
	ActionError(w, status, code)
}
