package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Extra DSM task claims and duplicate selectors never become backend input.
func TestSchedulerStatusSelectors(t *testing.T) {
	mux := http.NewServeMux()
	registerSchedulerStatus(mux, nil)
	for _, q := range []string{"", "profileId=../x", "profileId=a&profileId=b", "profileId=a&enabled=true", "profileId=a&nextRun=tomorrow"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/scheduler-status?"+q, nil))
		if w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
}
