package diagnostics

import (
	"context"
	"errors"
	"time"
)

type timingKey struct{}

// WithTimingSink carries only a nonblocking classified event sink, never request data.
func WithTimingSink(ctx context.Context, sink func(Event)) context.Context {
	return context.WithValue(ctx, timingKey{}, sink)
}

// Measure records fixed operation names and numeric counters while keeping errors private.
func Measure(ctx context.Context, phase string, count uint64) func(error) {
	started := time.Now()
	sink, _ := ctx.Value(timingKey{}).(func(Event))
	return func(err error) {
		if sink == nil {
			return
		}
		code := "QUERY_COMPLETED"
		if err != nil {
			code = "QUERY_FAILED"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "QUERY_TIMEOUT"
		}
		event := Event{Code: code, Phase: phase, Count: count, ElapsedMillis: uint64(time.Since(started).Milliseconds())}
		if valid(event) {
			sink(event)
		}
	}
}
