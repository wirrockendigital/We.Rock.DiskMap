package service

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// Wrapped metadata deadline/cancellation errors retain a safe JSON availability response.
func TestStorageDeadlineResponse(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)} {
		out := httptest.NewRecorder()
		storageError(out, err)
		if out.Code != 503 || !strings.Contains(out.Body.String(), `"code":"ACTION_BUDGET_EXCEEDED"`) || out.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected error response: %d %s", out.Code, out.Body.String())
		}
	}
}
