package diagnostics

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Timing events survive cancellation and expose neither errors nor arbitrary phase text.
func TestTimingPrivacyAndVocabulary(t *testing.T) {
	var events []Event
	ctx, cancel := context.WithCancel(WithTimingSink(context.Background(), func(e Event) { events = append(events, e) }))
	finish := Measure(ctx, "scope_recycle", 32)
	cancel()
	finish(errors.New("secret /private/path"))
	Measure(ctx, "private/path", 1)(nil)
	if len(events) != 1 || events[0].Code != "QUERY_FAILED" || events[0].Phase != "scope_recycle" || events[0].Count != 32 {
		t.Fatalf("unexpected classified timing: %+v", events)
	}
	logger := fixture(t)
	if err := logger.Record(events[0]); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(events[0].Code+events[0].Phase, "secret") {
		t.Fatal("error text leaked")
	}
	Measure(context.Background(), "map_catalog", 0)(nil)
	Measure(ctx, "scope_batch", 1)(context.DeadlineExceeded)
	if events[len(events)-1].Code != "QUERY_TIMEOUT" {
		t.Fatal("deadline was not classified")
	}
}
