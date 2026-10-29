package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Invalid batch payloads are rejected before any manager or database access.
func TestProfileSummariesStrictRequest(t *testing.T) {
	mux := http.NewServeMux()
	registerProfileSummaries(mux, nil)
	for _, body := range []string{`{"contractVersion":1,"profileIds":[]}`, `{"contractVersion":1,"profileIds":["p"],"rootPath":"/private"}`, `{"ContractVersion":1,"profileIds":["p"]}`, `{"contractVersion":1,"profileIds":["p","p"]}`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/profile-summaries", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
