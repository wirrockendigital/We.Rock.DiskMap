package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestExclusionPreviewProtocol rejects ambiguous schemas before accessing a manager.
func TestExclusionPreviewProtocol(t *testing.T) {
	mux := http.NewServeMux()
	registerExclusions(mux, nil)
	for _, body := range []string{`{"ContractVersion":1}`, `{"contractVersion":1,"exclusions":[{"Kind":"path","value":"x"}]}`, `{"contractVersion":1,"contractVersion":1}`, strings.Repeat(" ", 65537)} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/api/v1/profiles/preview-exclusions", strings.NewReader(body))
		mux.ServeHTTP(response, request)
		if response.Code != 400 {
			t.Fatal(response.Code, response.Body.String())
		}
	}
}
