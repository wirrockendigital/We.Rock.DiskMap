package jobs

import (
	"context"
	"errors"
	"syscall"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// Diagnostics retain operation identity while keeping private causes out of public history.
func TestScannerFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		cause error
		want  string
	}{
		{errors.New("private source pathname"), "SCAN_NODE_WRITE_FAILED"},
		{index.ErrGeneration, "SCAN_NODE_WRITE_FAILED_INVALID_GENERATION"},
		{context.Canceled, "SCAN_CANCELLED"},
		{syscall.ENOSPC, "INDEX_SPACE_EXHAUSTED"},
	} {
		if got := executionFailure(&scanner.Failure{Operation: "NODE_WRITE", Cause: tc.cause}); got != tc.want {
			t.Fatalf("got %s, want %s", got, tc.want)
		}
	}
}
