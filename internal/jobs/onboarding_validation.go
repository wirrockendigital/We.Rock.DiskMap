package jobs

import (
	"encoding/json"
	"reflect"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// ValidOnboardingState validates the durable receipt independently of transport ownership.
func ValidOnboardingState(s OnboardingState) bool { return validOnboardingState(s) }

// ValidOnboardingReceipt binds operation-specific fields to the submitted immutable intent.
func ValidOnboardingReceipt(s OnboardingState, r OnboardingRequest) bool {
	if r.Validate() != nil || !validOnboardingState(s) || s.Revision != r.ExpectedRevision+1 {
		return false
	}
	switch r.Change.Operation {
	case "replace":
		return s.Pending == nil && reflect.DeepEqual(s.Draft, r.Change.Draft)
	case "reset":
		return s.Pending == nil && s.Draft == nil
	case "prepareProfileCreate":
		raw, _ := json.Marshal(r.Change.Create.Request)
		return s.Pending != nil && s.Pending.Key == r.Change.Create.Key && s.Pending.CanonicalPayload == string(raw)
	case "acknowledgeProfileRejection":
		return s.Pending == nil && s.Draft != nil && s.Draft.ProfileID == nil
	case "acknowledgeProfileCreate":
		return s.Pending == nil && s.Draft != nil && s.Draft.ProfileID != nil
	}
	return false
}

// ValidOnboardingResponse rejects fabricated recovery states and verifies canonical create receipts.
func ValidOnboardingResponse(v OnboardingResponse) bool {
	s := OnboardingState{Version: v.Version, Revision: v.Revision, Draft: v.Draft}
	if !oneOf(v.ProfileState, "none", "active", "inactive", "missing") {
		return false
	}
	hasProfile := v.Draft != nil && v.Draft.ProfileID != nil
	if hasProfile == (v.ProfileState == "none") {
		return false
	}
	if v.Pending != nil {
		p := v.Pending
		s.Pending = &PendingOnboardingCreate{Key: p.Key, CanonicalPayload: p.CanonicalPayload}
		in, e := decodeOnboardingCreate(s.Pending)
		if e != nil {
			return false
		}
		if p.Status != "rejected" && p.RejectionCode != "" {
			return false
		}
		switch p.Status {
		case "rejected":
			if p.Receipt != nil || p.ProfileState != "unknown" || onboardingRejectionError(p.RejectionCode) == nil {
				return false
			}
		case "pending":
			if p.Receipt != nil || p.ProfileState != "unknown" {
				return false
			}
		case "committed", "profile-unavailable":
			if !validOnboardingCreateReceipt(in, p.Receipt) {
				return false
			}
			if p.Status == "committed" && p.ProfileState != "active" || p.Status == "profile-unavailable" && !oneOf(p.ProfileState, "missing", "inactive", "changed") {
				return false
			}
		default:
			return false
		}
	}
	raw, e := json.Marshal(v)
	return validOnboardingState(s) && e == nil && len(raw) <= 61440
}

// This comparison never uses today's mutable profile as proof of an earlier create result.
func validOnboardingCreateReceipt(in profiles.ActionRequest, r *ProfileActionResult) bool {
	if r == nil || r.RejectionCode != "" || r.Operation != "create" || r.Profile == nil || r.ProfileID != r.Profile.ID || r.Profile.Revision != 1 || r.CleanupScheduled || r.TasksRequireReview {
		return false
	}
	expected := in.Configuration.Profile(r.ProfileID)
	expected.Revision = 1
	known := map[string]bool{}
	for _, id := range r.Profile.Scope.TargetIDs {
		known[id] = true
	}
	return profiles.Validate(*r.Profile, known) == nil && reflect.DeepEqual(expected, *r.Profile)
}

// Reserve enough serialized space for a future create receipt before committing an unresolved intent.
func onboardingRecoveryFits(s OnboardingState) bool {
	if s.Pending == nil {
		return true
	}
	in, e := decodeOnboardingCreate(s.Pending)
	if e != nil {
		return false
	}
	p := in.Configuration.Profile("onboarding-reserved-profile-identity-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	p.Revision = 1
	out := OnboardingResponse{Version: 1, Revision: s.Revision, Draft: s.Draft, ProfileState: "none", Pending: &OnboardingRecovery{Key: s.Pending.Key, CanonicalPayload: s.Pending.CanonicalPayload, Status: "profile-unavailable", ProfileState: "inactive", Receipt: &ProfileActionResult{Operation: "create", ProfileID: p.ID, Profile: &p}}}
	raw, e := json.Marshal(out)
	return e == nil && len(raw) <= 61440
}
