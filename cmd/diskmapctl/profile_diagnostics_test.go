package main

import (
	"bytes"
	"strings"
	"testing"
)

// Fixed diagnostics never repeat arbitrary backend text or pretend that an unqualified 404 proves a missing profile.
func TestProfileRejectionDiagnostics(t *testing.T) {
	generic := "REQUEST_REJECTED: request rejected; check the active profile and copy its current task template"
	valid := `{"contractVersion":1,"error":{"code":"INVALID_REQUEST","messageKey":"private-secret","requestId":"private-path"}}`
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{400, valid, "SCAN_PROFILE_REJECTED: profile is missing, inactive or not valid for scanning; check it in the app and copy the current task template"},
		{404, strings.Replace(valid, "INVALID_REQUEST", "PROFILE_NOT_FOUND", 1), "PROFILE_NOT_FOUND: profile not found; select an existing active profile and copy its current task template"},
		{404, valid, generic}, {400, "", generic}, {404, "", generic}, {400, `{"contractVersion":2,"error":{"code":"INVALID_REQUEST"}}`, generic},
		{400, valid + `{}`, generic}, {400, strings.Replace(valid, `"code":"INVALID_REQUEST"`, `"code":"INVALID_REQUEST","code":"PRIVATE"`, 1), generic},
		{400, strings.Repeat("private-secret", 400), generic},
	} {
		got := rejectionDiagnostic("POST", "/api/v1/jobs", tc.status, []byte(tc.body))
		if got != tc.want || strings.Contains(got, "private-") {
			t.Fatalf("status %d diagnostic %q", tc.status, got)
		}
	}
	if got := rejectionDiagnostic("GET", "/api/v1/jobs/private-id", 404, []byte(valid)); got != generic {
		t.Fatal(got)
	}
}

// Local argument errors are identified before transport and never echo the supplied ID or mode.
func TestScanArgumentDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		prefix string
	}{
		{[]string{"scan"}, "INVALID_PROFILE_ID:"},
		{[]string{"scan", "--profile-id=private/path"}, "INVALID_PROFILE_ID:"},
		{[]string{"scan", "--profile-id=valid", "--mode=private-secret"}, "INVALID_SCAN_MODE:"},
	} {
		var out, diag bytes.Buffer
		code := run(append([]string{"--socket=/nonexistent/private-socket"}, tc.args...), nil, &out, &diag)
		if code != 2 || out.Len() != 0 || !strings.HasPrefix(diag.String(), tc.prefix) || strings.Contains(diag.String(), "private") {
			t.Fatal(code, diag.String())
		}
	}
}
