package security

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mutationRequest supplies synthetic credentials; the session guard verifies real credentials independently.
func mutationRequest() *http.Request {
	r := httptest.NewRequest("POST", "https://nas.test:5001/api.cgi?action=scan", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://nas.test:5001")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-SYNO-TOKEN", "synthetic")
	r.Header.Set("Idempotency-Key", "intent-1")
	return r
}

// TestMutationBoundary rejects foreign browser requests before state changes.
func TestMutationBoundary(t *testing.T) {
	for _, origin := range []string{"", "null", "http://nas.test:5001", "https://evil.test", "https://nas.test:5001.evil.test", "https://nas.test:5001/"} {
		r := mutationRequest()
		r.Header.Set("Origin", origin)
		if CheckMutation(r, "https://nas.test:5001") == nil {
			t.Fatal("accepted origin", origin)
		}
	}
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Method = "GET" }, func(r *http.Request) { r.Header.Add("Origin", "https://nas.test:5001") }, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, func(r *http.Request) { r.Header.Del("X-SYNO-TOKEN") }, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("k", 129)) }, func(r *http.Request) { r.Header.Add("Idempotency-Key", "other") }} {
		r := mutationRequest()
		mutate(r)
		if CheckMutation(r, "https://nas.test:5001") == nil {
			t.Fatal("accepted invalid mutation")
		}
	}
	if CheckMutation(mutationRequest(), "https://nas.test:5001") != nil {
		t.Fatal("valid rejected")
	}
	if CheckMutation(mutationRequest(), "") == nil {
		t.Fatal("missing origin accepted")
	}
}

// TestStrictSchema rejects ambiguous input, unbounded payloads and path-shaped IDs.
func TestStrictSchema(t *testing.T) {
	for _, body := range []string{`{"contractVersion":1,"profileId":"p","mode":"full","roots":["/etc"]}`, `{"contractVersion":1,"profileId":"p","profileId":"q","mode":"full"}`, `{"contractVersion":1,"profileId":"p","mode":"full"} {}`, strings.Repeat(" ", 4097), `{"x":` + strings.Repeat("[", 34) + `0` + strings.Repeat("]", 34) + `}`} {
		var s ScanRequest
		if DecodeJSON(strings.NewReader(body), 4096, &s) == nil {
			t.Fatal("accepted ambiguity")
		}
	}
	for _, id := range []string{"", "../root", "x' OR 1=1--", strings.Repeat("a", 129)} {
		if (ScanRequest{1, id, "full"}).Validate() == nil {
			t.Fatal("accepted ID")
		}
	}
	var s ScanRequest
	if DecodeJSON(strings.NewReader(`{"mode":"full","profileId":"p-1","contractVersion":1}`), 4096, &s) != nil || s.Validate() != nil {
		t.Fatal("valid rejected")
	}
}

// TestOriginMetadata allows only direct HTTPS CGI metadata.
func TestOriginMetadata(t *testing.T) {
	for _, host := range []string{"", "nas.test@evil.test", "nas.test/path", "nas.test#frag", "nas.test:invalid"} {
		if TrustedOrigin("on", host) != "" {
			t.Fatal("invalid authority")
		}
	}
	if TrustedOrigin("off", "nas.test") != "" || TrustedOrigin("on", "nas.test:5001") != "https://nas.test:5001" {
		t.Fatal("bad origin")
	}
}

// TestScanModes accepts only explicit V1 intentions through the shared mutation schema.
func TestScanModes(t *testing.T) {
	for _, mode := range []string{"full", "rebuild", "reconcile"} {
		if (ScanRequest{1, "profile", mode}).Validate() != nil {
			t.Fatal(mode)
		}
	}
	for _, mode := range []string{"refresh", "incremental", "FULL", "", " reconcile"} {
		if (ScanRequest{1, "profile", mode}).Validate() == nil {
			t.Fatal(mode)
		}
	}
}
