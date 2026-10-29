package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Recovery must reject caller-provided mappings and ambiguous identities before reaching the manager.
func TestNativeRequalificationRejectsUntrustedMappings(t *testing.T) {
	mux := http.NewServeMux()
	registerNativeRequalification(mux, nil)
	for _, body := range []string{
		`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"path":"/replacement"}`,
		`{"contractVersion":1,"profileId":"probe","expectedRevision":1,"bootId":"forged"}`,
		`{"contractVersion":1,"profileId":"../probe","expectedRevision":1}`,
		`{"contractVersion":1,"profileId":"probe","expectedRevision":0}`,
		`{"contractVersion":2,"profileId":"probe","expectedRevision":1}`,
		strings.Repeat(" ", 2049),
	} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/profiles/requalify-native", strings.NewReader(body))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 400 || strings.Contains(w.Body.String(), "replacement") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
