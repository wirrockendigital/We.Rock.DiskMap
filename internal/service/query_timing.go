package service

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
)

// queryTimingSink keeps disk I/O outside request latency and bounds both backlog and event frequency.
func queryTimingSink(ctx context.Context, record func(diagnostics.Event) error) func(diagnostics.Event) {
	queue := make(chan diagnostics.Event, 64)
	var mu sync.Mutex
	last := map[string]time.Time{}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-queue:
				// A logging failure cannot change business replies or trigger a query retry.
				_ = record(event)
			}
		}
	}()
	return func(event diagnostics.Event) {
		key := event.Code + ":" + event.Phase
		mu.Lock()
		defer mu.Unlock()
		if ctx.Err() != nil || time.Since(last[key]) < 5*time.Second {
			return
		}
		select {
		case queue <- event:
			last[key] = time.Now()
		default:
			// Saturated diagnostics drop samples instead of retaining request data or blocking readers.
		}
	}
}

type timingResponse struct {
	http.ResponseWriter
	status int
}

// WriteHeader preserves the first HTTP status without retaining a response body.
func (w *timingResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

// Write observes implicit success while forwarding bytes unchanged.
func (w *timingResponse) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(raw)
}

// traceQueries measures only exact internal read routes; query strings and identities are never logged.
func traceQueries(next http.Handler, sink func(diagnostics.Event)) http.Handler {
	phases := map[string]string{
		"/api/v1/scope-observations": "scope_batch", "/api/v1/hierarchy": "hierarchy",
		"/api/v1/map-manifest": "map_manifest", "/api/v1/map-chunks": "map_chunks", "/api/v1/views": "reader",
		"/api/v1/inventory": "inventory", "/api/v1/scope-inventory": "inventory",
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		phase, known := phases[r.URL.Path]
		if !known {
			next.ServeHTTP(w, r)
			return
		}
		ctx := diagnostics.WithTimingSink(r.Context(), sink)
		finish := diagnostics.Measure(ctx, phase, 0)
		response := &timingResponse{ResponseWriter: w}
		next.ServeHTTP(response, r.WithContext(ctx))
		var err error
		if response.status >= 400 {
			err = context.Canceled // Classification only; response text and backend errors remain private.
		}
		finish(err)
	})
}
