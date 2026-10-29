package main

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scanRequest intentionally forges a principal to prove the proxy replaces it.
func scanRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "https://nas.test:5001/api.cgi?action=scan", strings.NewReader(body))
	r.Header.Set("Origin", "https://nas.test:5001")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-SYNO-TOKEN", "synthetic")
	r.Header.Set("Idempotency-Key", "intent-1")
	r.Header.Set("X-DiskMap-Principal", "forged")
	return r
}

// TestScanBoundary keeps mutations disabled until persistent service replay is integrated.
func TestScanBoundary(t *testing.T) {
	calls := 0
	b := bridge{identity: func(context.Context) (string, error) { return "verified-admin", nil }, expectedOrigin: "https://nas.test:5001", client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/api/v1/jobs" || r.Header.Get("X-DiskMap-Principal") != "verified-admin" || r.Header.Get("Idempotency-Key") != "intent-1" || r.Header.Get("X-SYNO-TOKEN") != "" {
			t.Fatal("unsafe headers")
		}
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"contractVersion":1,"profileId":"p","mode":"full"}` {
			t.Fatal("wrong payload")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"contractVersion":1,"data":{"jobId":"j"}}`))}, nil
	})}}
	body := `{"contractVersion":1,"profileId":"p","mode":"full"}`
	w := httptest.NewRecorder()
	b.ServeHTTP(w, scanRequest(body))
	if w.Code != 503 || calls != 0 {
		t.Fatal("disabled escaped")
	}
	b.mutationsEnabled = true
	r := scanRequest(body)
	r.Header.Set("Origin", "https://evil.test")
	w = httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal("foreign escaped")
	}
	w = httptest.NewRecorder()
	b.ServeHTTP(w, scanRequest(`{"contractVersion":1,"profileId":"../etc","mode":"full"}`))
	if w.Code != 400 || calls != 0 {
		t.Fatal("invalid escaped")
	}
	w = httptest.NewRecorder()
	b.ServeHTTP(w, scanRequest(body))
	if w.Code != 200 || calls != 1 {
		t.Fatal(w.Code, calls)
	}
}

// changingReader revokes authorization after the initial guard but before body decoding finishes.
type changingReader struct {
	reader io.Reader
	change func()
}

func (r changingReader) Read(p []byte) (int, error) { r.change(); return r.reader.Read(p) }

// TestBodyReadRevocation proves session loss prevents the side effect, not only its response.
func TestBodyReadRevocation(t *testing.T) {
	for _, role := range []bool{false, true} {
		valid := true
		calls := 0
		b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) {
			if !valid {
				if role {
					return "", dsmauth.ErrRole
				}
				return "", dsmauth.ErrSession
			}
			return "admin", nil
		}, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			calls++
			t.Fatal("revoked body reached service")
			return nil, nil
		})}}
		r := scanRequest(`{"contractVersion":1,"profileId":"p","mode":"full"}`)
		r.Body = io.NopCloser(changingReader{r.Body, func() { valid = false }})
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		expected := 401
		if role {
			expected = 403
		}
		if w.Code != expected || calls != 0 {
			t.Fatal(w.Code, calls)
		}
	}
}

// TestConflictCodes allows only bounded versioned service conflicts to reach the browser.
func TestConflictCodes(t *testing.T) {
	for _, tc := range []struct {
		body     string
		code     int
		contains string
	}{
		{`{"contractVersion":1,"error":{"code":"JOB_CONFLICT","messageKey":"JOB_CONFLICT","requestId":"local","retryable":false}}`, 409, "JOB_CONFLICT"},
		{`{"contractVersion":1,"error":{"code":"IDEMPOTENCY_CONFLICT"}}`, 409, "IDEMPOTENCY_CONFLICT"},
		{`{"contractVersion":1,"error":{"code":"QUEUE_FULL"}}`, 409, "QUEUE_FULL"},
		{`{"contractVersion":2,"error":{"code":"JOB_CONFLICT"}}`, 502, "SERVICE_RESPONSE_INVALID"},
		{`{"contractVersion":1,"error":{"code":"/private/path"}}`, 502, "SERVICE_RESPONSE_INVALID"},
		{strings.Repeat("x", 4097), 502, "SERVICE_RESPONSE_INVALID"},
	} {
		b := bridge{mutationsEnabled: true, expectedOrigin: "https://nas.test:5001", identity: func(context.Context) (string, error) { return "admin", nil }, client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 409, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
		})}}
		w := httptest.NewRecorder()
		b.ServeHTTP(w, scanRequest(`{"contractVersion":1,"profileId":"p","mode":"full"}`))
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
