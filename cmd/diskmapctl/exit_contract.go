package main

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ExitContractVersion versions CLI process results independently from the service envelope schema.
const ExitContractVersion = 1

// cliResult adds only CLI provenance; the embedded service data retains its canonical shape.
type cliResult struct {
	contracts.Envelope[jobs.Submission]
	ExitContractVersion int `json:"exitContractVersion"`
}

// terminalExit preserves failure precedence when a caller joins an existing job.
// Unknown states or impossible snapshot bindings are invalid protocol, never successful completion.
func terminalExit(o jobs.Outcome, deduplicated bool) (code int, waiting bool) {
	switch o.Job.State {
	case "queued", "preflight", "scanning", "paused", "aggregating", "validating", "publishing":
		if o.Job.ResultSnapshotID != nil || o.SnapshotState != "" {
			return 4, false
		}
		return 0, true
	case "completed":
		if o.Job.ResultSnapshotID == nil || !security.ValidID(*o.Job.ResultSnapshotID) {
			return 4, false
		}
		if o.SnapshotState == "partial" {
			return 7, false
		}
		if o.SnapshotState != "complete" {
			return 4, false
		}
		if deduplicated {
			return 8, false
		}
		return 0, false
	case "cancelled", "failed", "interrupted":
		if o.Job.ResultSnapshotID != nil || o.SnapshotState != "" {
			return 4, false
		}
		if o.Job.State == "cancelled" {
			return 9, false
		}
		return 6, false
	default:
		return 4, false
	}
}
