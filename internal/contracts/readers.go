package contracts

import (
	"errors"
	"regexp"
)

var readerID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var errReaderRequest = errors.New("INVALID_REQUEST")

// ViewRequest controls only bounded ephemeral read leases; it never starts filesystem work.
type ViewRequest struct {
	ContractVersion int    `json:"contractVersion"`
	Operation       string `json:"operation"`
	ProfileID       string `json:"profileId"`
	ViewID          string `json:"viewId"`
	JobID           string `json:"jobId,omitempty"`
	NewViewID       string `json:"newViewId,omitempty"`
	SnapshotID      string `json:"snapshotId,omitempty"`
	NodeID          string `json:"nodeId,omitempty"`
}

// Validate limits the exact operation fields before any lease allocation or lookup.
func (r ViewRequest) Validate() error {
	valid := func(s string) bool { return readerID.MatchString(s) }
	if r.ContractVersion != 1 || !valid(r.ProfileID) || !valid(r.ViewID) {
		return errReaderRequest
	}
	switch r.Operation {
	case "open":
		if r.NewViewID != "" || r.NodeID != "" || (r.JobID != "" && !valid(r.JobID)) || (r.SnapshotID != "" && (!valid(r.SnapshotID) || r.JobID != "")) {
			return errReaderRequest
		}
	case "read", "release":
		if r.JobID != "" || r.NewViewID != "" || r.SnapshotID != "" || r.NodeID != "" {
			return errReaderRequest
		}
	case "switch":
		if r.JobID != "" || !valid(r.NewViewID) || r.NewViewID == r.ViewID || !valid(r.SnapshotID) || (r.NodeID != "" && !valid(r.NodeID)) {
			return errReaderRequest
		}
	default:
		return errReaderRequest
	}
	return nil
}
