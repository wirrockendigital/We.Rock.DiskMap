// Package security owns the authorization boundary for every public application data route.
package security

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
)

// Identity resolves the current authorized DSM account on every call.
type Identity func(context.Context) (string, error)

type principalKey struct{}

// Principal returns only an identity placed by the successful session guard.
func Principal(ctx context.Context) string {
	name, _ := ctx.Value(principalKey{}).(string)
	return name
}

// responseBuffer prevents disclosure before the final session check completes.
type responseBuffer struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
	limit    int
}

func (b *responseBuffer) Header() http.Header { return b.header }
func (b *responseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *responseBuffer) Write(value []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	limit := b.limit
	if limit == 0 {
		limit = 64 * 1024
	}
	if b.overflow || b.body.Len()+len(value) > limit {
		b.overflow = true
		return 0, errors.New("response limit exceeded")
	}
	return b.body.Write(value)
}

// Protect fails closed before routing and after bounded response generation; it never streams private data.
func Protect(identity Identity, next http.Handler) http.Handler {
	return ProtectBounded(identity, next, 64*1024)
}

// MaxGuardedResponseBytes caps server-selected data responses, including their envelopes.
const MaxGuardedResponseBytes = (4 << 20) + 4096

// ProtectBounded takes a server-selected limit; final authorization precedes every private byte.
func ProtectBounded(identity Identity, next http.Handler, limit int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		PrivacyHeaders(w.Header())
		if limit < 1 || limit > MaxGuardedResponseBytes {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if token := r.Header.Get("X-SYNO-TOKEN"); token == "" || len(token) > 4096 {
			deny(w, dsmauth.ErrSession)
			return
		}
		initial, err := identity(r.Context())
		if err != nil || initial == "" {
			deny(w, err)
			return
		}
		result := &responseBuffer{header: make(http.Header), limit: limit}
		next.ServeHTTP(result, r.WithContext(context.WithValue(r.Context(), principalKey{}, initial)))
		current, err := identity(r.Context())
		if err != nil || current != initial || r.Context().Err() != nil {
			deny(w, err)
			return
		}
		if result.overflow {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		// Only explicit response metadata crosses the boundary; cookies and redirects cannot leak through.
		for _, key := range []string{"Content-Type", "Allow"} {
			if value := result.header.Get(key); value != "" {
				w.Header().Set(key, value)
			}
		}
		if result.status == 0 {
			result.status = http.StatusOK
		}
		w.WriteHeader(result.status)
		_, _ = w.Write(result.body.Bytes())
	})
}

// deny exposes no identity, path, role membership, or credential value.
func deny(w http.ResponseWriter, err error) {
	// A fixed length permits complete denial delivery before the CGI response closes.
	const body = `{"error":"access_denied"}`
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if errors.Is(err, dsmauth.ErrRole) {
		w.WriteHeader(http.StatusForbidden)
	} else {
		w.WriteHeader(http.StatusUnauthorized)
	}
	_, _ = w.Write([]byte(body))
}
