package scanner

import (
	"context"
	"time"
)

// Progress contains measured metadata counters; it never implies a known scan total.
type Progress struct {
	Entries           uint64 `json:"entries"`
	Files             uint64 `json:"files"`
	Directories       uint64 `json:"directories"`
	Specials          uint64 `json:"specials"`
	UnknownLogical    uint64 `json:"unknownLogical"`
	KnownLogicalBytes string `json:"knownLogicalBytes"`
	Warnings          uint64 `json:"warnings"`
	TargetID          string `json:"targetId"`
}
type progressKey struct{}

// WithProgress observes bounded samples without changing scan correctness or cancellation.
func WithProgress(ctx context.Context, observer func(Progress)) context.Context {
	return context.WithValue(ctx, progressKey{}, observer)
}

// progressReporter defers DTO formatting until delivery; final samples are always delivered.
func progressReporter(ctx context.Context) func(func() Progress, bool) {
	observer, _ := ctx.Value(progressKey{}).(func(Progress))
	var last time.Time
	return func(sample func() Progress, final bool) {
		if observer != nil && (final || time.Since(last) >= time.Second) {
			observer(sample())
			// Measure the quiet interval after delivery, including a slow observer.
			last = time.Now()
		}
	}
}
