package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Malformed scopes and missing trusted principals fail before accessing manager state.
func TestScanPreviewRequestBoundary(t *testing.T) {
	mux := http.NewServeMux()
	registerScanPreview(mux, nil)
	for _, target := range []string{"/api/v1/scan-preview?jobId=job&profileId=profile", "/api/v1/scan-preview?jobId=job", "/api/v1/scan-preview?jobId=job&jobId=other&profileId=profile", "/api/v1/scan-preview?jobId=job&profileId=profile&principal=admin"} {
		request := httptest.NewRequest("GET", target, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatal(target, response.Code)
		}
	}
}
