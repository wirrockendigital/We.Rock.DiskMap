package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// wireManifest shadows only the new private authority field without mutating the stored manifest.
// Existing public job fields keep their established wire shape.
type wireManifest struct {
	scopemanifest.ScopeManifest
	Provenance *struct{} `json:"provenance,omitempty"`
}

// wireOutcome preserves legacy job transport while keeping new private source authority off the wire.
type wireOutcome struct {
	jobs.Outcome
	ScopeManifest *wireManifest `json:"scopeManifest"`
}

// publicOutcome copies the outer value; neither callers nor serialization edit shared containers.
func publicOutcome(out jobs.Outcome) wireOutcome {
	result := wireOutcome{Outcome: out}
	if out.ScopeManifest != nil {
		result.ScopeManifest = &wireManifest{ScopeManifest: *out.ScopeManifest}
	}
	return result
}

// publicSubmission retains the immutable deduplication receipt alongside the redacted outcome.
func publicSubmission(out jobs.Submission) any {
	return struct {
		wireOutcome
		Deduplicated bool `json:"deduplicated"`
	}{publicOutcome(out.Outcome), out.Deduplicated}
}
