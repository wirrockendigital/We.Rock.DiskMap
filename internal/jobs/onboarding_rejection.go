package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Only these final domain decisions may reserve a prepared create key as rejected.
func onboardingRejectionError(code string) error {
	switch code {
	case "INVALID_REQUEST":
		return ErrInvalid
	case "JOB_CONFLICT":
		return ErrConflict
	case "PROFILE_REBIND_REQUIRED":
		return ErrRootRebind
	}
	return nil
}

// Rejections contain no successful domain result or user-controlled diagnostics.
func validOnboardingRejection(r ProfileActionResult) bool {
	return r.Operation == "create" && r.ProfileID == "" && r.Profile == nil && !r.CleanupScheduled && !r.TasksRequireReview && onboardingRejectionError(r.RejectionCode) != nil
}

// The original transaction serializes rejection with concurrent create and acknowledgment.
func persistOnboardingRejection(ctx context.Context, tx *sql.Tx, replay ReplayRequest, cause error) error {
	code := ""
	for _, c := range []string{"INVALID_REQUEST", "JOB_CONFLICT", "PROFILE_REBIND_REQUIRED"} {
		if errors.Is(cause, onboardingRejectionError(c)) {
			code = c
			break
		}
	}
	if code == "" || ctx.Err() != nil {
		return cause
	}
	if _, e := tx.ExecContext(ctx, "ROLLBACK TO onboarding_create"); e != nil {
		return e
	}
	s, e := readOnboarding(ctx, tx, replay.Principal)
	if e != nil {
		return e
	}
	if s.Pending == nil || s.Pending.Key != replay.Key || s.Pending.CanonicalPayload != replay.Payload {
		return cause
	}
	raw, e := json.Marshal(ProfileActionResult{Operation: "create", RejectionCode: code})
	if e != nil {
		return e
	}
	if e = RecordReplay(tx, replay, raw); e != nil {
		return e
	}
	if e = CommitReplay(tx, nil); e != nil {
		return e
	}
	return cause
}
