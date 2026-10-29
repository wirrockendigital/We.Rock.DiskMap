package service

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Admission budget expiry and SQLite contention both remain sanitized public
// 503 responses, with distinct fixed codes rather than internal-error HTML.
func TestHierarchyAdmissionUnavailableMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{
		{"deadline", context.DeadlineExceeded, "QUERY_BUDGET_EXCEEDED"},
		{"sqlite-unavailable", jobs.ErrUnavailable, "SERVICE_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			hierarchyError(w, tc.err)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if w.Code != 503 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Error.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
