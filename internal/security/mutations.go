package security

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsmauth"
	"net/http"
	"unicode/utf8"
)

// ValidatedMutation is the shared boundary that every future feature action must implement.
type ValidatedMutation interface{ Validate() error }

// Mutate validates a bounded action and rechecks identity before invoking its real owner.
// Test handlers prove this boundary; they do not implement application pause or profile actions.
func Mutate(w http.ResponseWriter, r *http.Request, origin string, identity func(context.Context) (string, error), input ValidatedMutation, apply func(principal, key string)) {
	limit := int64(16384)
	if _, scan := input.(*ScanRequest); scan {
		limit = 4096
	}
	MutateBounded(w, r, origin, identity, input, limit, apply)
}

// MutateBounded accepts a server-selected ceiling for a specifically bounded request contract.
// Clients cannot choose this limit; origin, schema and final identity checks remain identical.
func MutateBounded(w http.ResponseWriter, r *http.Request, origin string, identity func(context.Context) (string, error), input ValidatedMutation, limit int64, apply func(principal, key string)) {
	if limit < 1 || limit > 256<<10 {
		ActionError(w, 500, "ACTION_UNAVAILABLE")
		return
	}
	mutateValidated(w, r, origin, identity, input, limit, apply)
}

// MutateConfigurationImport uses only the fixed bulk-configuration contract ceiling.
// Existing mutation limits stay unchanged, and clients cannot choose an arbitrary larger limit.
func MutateConfigurationImport(w http.ResponseWriter, r *http.Request, origin string, identity func(context.Context) (string, error), input ValidatedMutation, apply func(principal, key string)) {
	mutateValidated(w, r, origin, identity, input, contracts.ConfigurationImportLimit, apply)
}

// mutateValidated is the single origin, body and current-identity validation path.
func mutateValidated(w http.ResponseWriter, r *http.Request, origin string, identity func(context.Context) (string, error), input ValidatedMutation, limit int64, apply func(principal, key string)) {
	if CheckMutation(r, origin) != nil {
		ActionError(w, 403, "REQUEST_FORBIDDEN")
		return
	}
	if DecodeJSON(r.Body, limit, input) != nil || input.Validate() != nil {
		ActionError(w, 400, "INVALID_REQUEST")
		return
	}
	principal := Principal(r.Context())
	current, err := identity(r.Context())
	if principal == "" || err != nil || current != principal || r.Context().Err() != nil {
		status := 401
		if errors.Is(err, dsmauth.ErrRole) {
			status = 403
		}
		ActionError(w, status, "REQUEST_FORBIDDEN")
		return
	}
	if apply == nil {
		ActionError(w, 503, "ACTION_UNAVAILABLE")
		return
	}
	apply(principal, r.Header.Get("Idempotency-Key"))
}

// JobActionRequest specifies a desired operation, never a claimed resulting job state.
type JobActionRequest struct {
	ContractVersion int    `json:"contractVersion"`
	JobID           string `json:"jobId"`
	Action          string `json:"action"`
}

// Validate limits job control to explicit opaque identities and the recorded action vocabulary.
func (r JobActionRequest) Validate() error {
	if r.ContractVersion != 1 || !ValidID(r.JobID) || (r.Action != "pause" && r.Action != "resume" && r.Action != "cancel" && r.Action != "cancel-queued") {
		return ErrInvalidRequest
	}
	return nil
}

// ProfileMutationRequest owns security preconditions; feature-specific configuration remains typed by its owner.
// No arbitrary path or unbounded configuration object crosses this header contract.
type ProfileMutationRequest struct {
	ContractVersion        int    `json:"contractVersion"`
	Operation              string `json:"operation"`
	ProfileID              string `json:"profileId"`
	ExpectedRevision       int    `json:"expectedRevision"`
	ConfirmOwnDataDeletion bool   `json:"confirmOwnDataDeletion"`
}

// Validate requires optimistic revisions and explicit confirmation for destructive own-data operations.
func (r ProfileMutationRequest) Validate() error {
	if r.ContractVersion != 1 {
		return ErrInvalidRequest
	}
	switch r.Operation {
	case "create":
		if r.ProfileID != "" || r.ExpectedRevision != 0 || r.ConfirmOwnDataDeletion {
			return ErrInvalidRequest
		}
	case "update", "duplicate", "deactivate", "delete":
		if !ValidID(r.ProfileID) || r.ExpectedRevision < 1 || r.ExpectedRevision > 2147483647 {
			return ErrInvalidRequest
		}
		if (r.Operation == "delete") != r.ConfirmOwnDataDeletion {
			return ErrInvalidRequest
		}
	default:
		return ErrInvalidRequest
	}
	return nil
}

// SearchInput constrains transport cost while retaining SQL-like text as literal data.
type SearchInput struct {
	SnapshotID string `json:"snapshotId"`
	Text       string `json:"text"`
	Limit      int    `json:"limit"`
}

// Validate does not remove SQL punctuation; the query owner must bind it as a parameter.
func (r SearchInput) Validate() error {
	if !ValidID(r.SnapshotID) || !utf8.ValidString(r.Text) || len(r.Text) > 4096 || r.Limit < 1 || r.Limit > 200 {
		return ErrInvalidRequest
	}
	return nil
}
