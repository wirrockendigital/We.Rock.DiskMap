package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

// TestPrivacyEveryExit covers successes, denial, route errors, and bounded-response failures.
func TestPrivacyEveryExit(t *testing.T) {
	for _, scenario := range []string{"success", "session", "role", "error", "overflow"} {
		t.Run(scenario, func(t *testing.T) {
			identity := func(context.Context) (string, error) {
				if scenario == "session" {
					return "", dsmauth.ErrSession
				}
				if scenario == "role" {
					return "", dsmauth.ErrRole
				}
				return "admin", nil
			}
			h := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "public,max-age=86400")
				w.Header().Set("Set-Cookie", "private=session")
				if scenario == "overflow" {
					_, _ = w.Write([]byte(strings.Repeat("x", 65537)))
					return
				}
				if scenario == "error" {
					w.WriteHeader(500)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			req := httptest.NewRequest("GET", "/api.cgi?action=session", nil)
			req.Header.Set("X-SYNO-TOKEN", "synthetic")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			for key, value := range map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache", "Expires": "0", "Referrer-Policy": "no-referrer", "Vary": "Cookie, X-SYNO-TOKEN", "X-Content-Type-Options": "nosniff"} {
				if w.Header().Get(key) != value {
					t.Fatalf("missing privacy header %s", key)
				}
			}
			if w.Header().Get("Set-Cookie") != "" {
				t.Fatal("backend cookie leaked")
			}
		})
	}
}

// TestFailureProjection discards private fields at every nesting level rather than guessing redaction patterns.
func TestFailureProjection(t *testing.T) {
	secret := `private-name /volume1/private/family password=synthetic SynoToken=synthetic`
	for _, status := range []int{400, 401, 403, 404, 409, 500, 503, 302} {
		w := httptest.NewRecorder()
		body := `{"contractVersion":1,"error":{"code":"PROFILE_REBIND_REQUIRED","messageKey":"` + secret + `","requestId":"` + secret + `","retryable":false}}`
		ReadServiceFailure(w, status, strings.NewReader(body))
		if strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "synthetic") || strings.Contains(w.Body.String(), "volume1") {
			t.Fatalf("private diagnostic leaked for %d", status)
		}
		if status == 409 && !strings.Contains(w.Body.String(), "PROFILE_REBIND_REQUIRED") {
			t.Fatal("public error semantics lost")
		}
	}
	w := httptest.NewRecorder()
	ReadServiceFailure(w, 409, strings.NewReader(`{"contractVersion":1,"error":{"code":"PRIVATE_NAME","nested":{"secret":"x"}}}`))
	if w.Code != 502 {
		t.Fatal("unrecognized diagnostic accepted")
	}
}
