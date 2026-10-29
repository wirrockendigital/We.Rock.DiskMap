package security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

// TestHierarchyResponseBudgetPreservesFinalAuthorization proves larger replies never bypass revocation.
func TestHierarchyResponseBudgetPreservesFinalAuthorization(t *testing.T) {
	for _, mode := range []string{"allowed", "revoked", "over-budget", "invalid-limit"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			limit := MaxGuardedResponseBytes
			if mode == "invalid-limit" {
				limit++
			}
			identity := func(context.Context) (string, error) {
				calls++
				if mode == "revoked" && calls == 2 {
					return "", dsmauth.ErrRole
				}
				return "admin", nil
			}
			handler := ProtectBounded(identity, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				size := 1 << 20
				if mode == "over-budget" {
					size = MaxGuardedResponseBytes + 1
				}
				_, _ = w.Write([]byte(strings.Repeat("private", size/7+1)))
			}), limit)
			r := httptest.NewRequest("POST", "/?action=hierarchy", nil)
			r.Header.Set("X-SYNO-TOKEN", "test")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 200
			switch mode {
			case "revoked":
				want = 403
			case "over-budget":
				want = 502
			case "invalid-limit":
				want = 500
			}
			if w.Code != want {
				t.Fatalf("status %d != %d", w.Code, want)
			}
			if mode != "allowed" && strings.Contains(w.Body.String(), "private") {
				t.Fatal("private output escaped")
			}
			if mode == "allowed" && w.Body.Len() < 1<<20 {
				t.Fatal("large approved result missing")
			}
			if mode != "invalid-limit" && calls != 2 {
				t.Fatal("final identity check missing")
			}
		})
	}
}
