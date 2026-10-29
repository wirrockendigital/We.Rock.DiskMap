package security

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Forged cookies do not bypass a missing token or the vendor identity result.
func TestDeniedBeforeRouting(t *testing.T) {
	for _, tc := range []struct {
		token   string
		failure error
		code    int
	}{{"", nil, 401}, {strings.Repeat("x", 4097), nil, 401}, {"forged", dsmauth.ErrSession, 401}, {"synthetic", dsmauth.ErrRole, 403}} {
		routeCalls := 0
		handler := Protect(func(context.Context) (string, error) { return "admin", tc.failure }, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { routeCalls++; _, _ = w.Write([]byte("private")) }))
		request := httptest.NewRequest("GET", "/?action=session", nil)
		request.Header.Set("X-SYNO-TOKEN", tc.token)
		request.Header.Set("Cookie", "id=forged")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.code || routeCalls != 0 || strings.Contains(response.Body.String(), "private") {
			t.Fatal(response.Code, routeCalls)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable denial")
		}
	}
}

// Role revocation and account switches during route execution both discard private output.
func TestRevocationAndBounds(t *testing.T) {
	for _, mode := range []string{"role-revoked", "account-switched", "session-ended", "oversized", "valid"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			identity := func(context.Context) (string, error) {
				calls++
				if calls == 2 {
					switch mode {
					case "role-revoked":
						return "", dsmauth.ErrRole
					case "account-switched":
						return "other", nil
					case "session-ended":
						return "", dsmauth.ErrSession
					}
				}
				return "admin", nil
			}
			handler := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Set-Cookie", "secret=forbidden")
				w.Header().Set("Cache-Control", "public")
				if mode == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat("p", 65537)))
				} else {
					_, _ = w.Write([]byte(`{"private":true}`))
				}
			}))
			request := httptest.NewRequest("GET", "/", nil)
			request.Header.Set("X-SYNO-TOKEN", "synthetic")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			expected := 401
			switch mode {
			case "role-revoked":
				expected = 403
			case "oversized":
				expected = 502
			case "valid":
				expected = 200
			}
			if response.Code != expected || calls != 2 {
				t.Fatal(response.Code, calls)
			}
			if mode != "valid" && strings.Contains(response.Body.String(), "private") {
				t.Fatal("private response escaped")
			}
			if response.Header().Get("Set-Cookie") != "" || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("unsafe response metadata escaped")
			}
		})
	}
}
