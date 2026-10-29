package security

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Only the fixed import wrapper may accept a body above the existing 256 KiB ceiling.
func TestConfigurationImportMutationBudget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mode, size int
		origin     string
		want       int
	}{
		{"import", 0, 300000, "https://nas.test", 204},
		{"bounded", 1, 300000, "https://nas.test", 400},
		{"ordinary", 2, 20000, "https://nas.test", 400},
		{"oversize", 0, contracts.ConfigurationImportLimit, "https://nas.test", 400},
		{"origin", 0, 300000, "https://other.test", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			identity := func(context.Context) (string, error) { return "admin", nil }
			h := Protect(identity, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var in JobActionRequest
				apply := func(owner, key string) {
					if owner != "admin" || key != "key" {
						t.Fatal("owner")
					}
					w.WriteHeader(204)
				}
				switch tc.mode {
				case 0:
					MutateConfigurationImport(w, r, "https://nas.test", identity, &in, apply)
				case 1:
					MutateBounded(w, r, "https://nas.test", identity, &in, 256*1024, apply)
				case 2:
					Mutate(w, r, "https://nas.test", identity, &in, apply)
				}
			}))
			r := httptest.NewRequest("POST", "/", strings.NewReader(strings.Repeat(" ", tc.size)+`{"contractVersion":1,"jobId":"j","action":"pause"}`))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "key")
			r.Header.Set("X-SYNO-TOKEN", "test")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
