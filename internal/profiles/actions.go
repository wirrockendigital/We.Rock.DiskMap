package profiles

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// Configuration deliberately has no client-assigned identity, revision or physical bindings.
type Configuration struct {
	Name             string                   `json:"name"`
	Scope            contracts.Scope          `json:"scope"`
	Active           bool                     `json:"active"`
	Exclusions       []contracts.Exclusion    `json:"exclusions"`
	ResourceMode     string                   `json:"resourceMode"`
	Limits           contracts.ResourceLimits `json:"limits"`
	FreshnessSeconds int                      `json:"freshnessSeconds"`
}

// Profile binds validated configuration to a server-owned technical identity.
func (c Configuration) Profile(id string) contracts.Profile {
	return contracts.Profile{ID: id, Name: c.Name, Scope: c.Scope, Active: c.Active, Exclusions: c.Exclusions, ResourceMode: c.ResourceMode, Limits: c.Limits, FreshnessSeconds: c.FreshnessSeconds}
}

// ActionRequest is the bounded public action schema, including explicit own-data/task confirmation.
type ActionRequest struct {
	ContractVersion        int            `json:"contractVersion"`
	Operation              string         `json:"operation"`
	ProfileID              string         `json:"profileId,omitempty"`
	ExpectedRevision       int            `json:"expectedRevision,omitempty"`
	Configuration          *Configuration `json:"configuration,omitempty"`
	Name                   string         `json:"name,omitempty"`
	ConfirmOwnDataDeletion bool           `json:"confirmOwnDataDeletion,omitempty"`
	AcknowledgeTasks       bool           `json:"acknowledgeTasks,omitempty"`
	ConfirmProfileName     string         `json:"confirmProfileName,omitempty"`
}

// Validate rejects cross-operation fields before authentication is rechecked at the mutation boundary.
func (r ActionRequest) Validate() error {
	if (security.ProfileMutationRequest{ContractVersion: r.ContractVersion, Operation: r.Operation, ProfileID: r.ProfileID, ExpectedRevision: r.ExpectedRevision, ConfirmOwnDataDeletion: r.ConfirmOwnDataDeletion}).Validate() != nil {
		return ErrInvalid
	}
	if r.Operation != "delete" && (r.AcknowledgeTasks || r.ConfirmProfileName != "") {
		return ErrInvalid
	}
	switch r.Operation {
	case "create", "update":
		if r.Configuration == nil || r.Name != "" {
			return ErrInvalid
		}
		known := map[string]bool{}
		for _, id := range r.Configuration.Scope.TargetIDs {
			known[id] = true
		}
		if Validate(r.Configuration.Profile("validation"), known) != nil {
			return ErrInvalid
		}
	case "duplicate":
		if r.Configuration != nil || r.Name == "" || len(r.Name) > 1024 {
			return ErrInvalid
		}
	case "deactivate":
		if r.Configuration != nil || r.Name != "" {
			return ErrInvalid
		}
	case "delete":
		if r.Configuration != nil || r.Name != "" || !r.AcknowledgeTasks || r.ConfirmProfileName == "" || len(r.ConfirmProfileName) > 1024 {
			return ErrInvalid
		}
	}
	return nil
}

// JobRequest defines intent only; the job manager must implement the requested mode explicitly.
// No refresh/rebuild request is translated into a full scan by this contract.
type JobRequest struct {
	ContractVersion  int    `json:"contractVersion"`
	ProfileID        string `json:"profileId"`
	ExpectedRevision int    `json:"expectedRevision"`
	Operation        string `json:"operation"`
}

// Validate keeps future job wiring versioned without claiming an implemented execution mode.
func (r JobRequest) Validate() error {
	if r.ContractVersion != 1 || !security.ValidID(r.ProfileID) || r.ExpectedRevision < 1 || (r.Operation != "refresh" && r.Operation != "rebuild") {
		return ErrInvalid
	}
	return nil
}
