package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
)

// Instrumentation must preserve wire replies and ignore unknown paths, selectors and credentials.
func TestTraceQueryClassificationAndReply(t *testing.T) {
	var events []diagnostics.Event
	for _, status := range []int{200, 503} {
		h := traceQueries(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			finish := diagnostics.Measure(r.Context(), "scope_recycle", 4)
			finish(nil)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte("unchanged"))
		}), func(e diagnostics.Event) { events = append(events, e) })
		r := httptest.NewRequest("POST", "/api/v1/scope-observations?private=secret", strings.NewReader("private body"))
		r.Header.Set("X-DiskMap-Principal", "private-user")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status || w.Body.String() != "unchanged" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("instrumentation changed reply")
		}
	}
	if len(events) != 4 || events[0].Phase != "scope_recycle" || events[1].Code != "QUERY_COMPLETED" || events[3].Code != "QUERY_FAILED" {
		t.Fatalf("unexpected timings: %+v", events)
	}
}

// A stalled log device cannot delay a request or create an unbounded queue of events.
func TestQueryTimingNonblockingAndRateLimited(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	sink := queryTimingSink(ctx, func(diagnostics.Event) error {
		close(started)
		<-release
		close(finished)
		return nil
	})
	sink(diagnostics.Event{Code: "QUERY_COMPLETED", Phase: "scope_batch"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10000; i++ {
			sink(diagnostics.Event{Code: "QUERY_COMPLETED", Phase: "scope_batch"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request blocked on logging")
	}
	cancel()
	close(release)
	<-finished
}
