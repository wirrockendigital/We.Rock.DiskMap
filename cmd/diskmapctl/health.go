package main

import (
	"bytes"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// healthReady requires an explicit, internally consistent readiness receipt.
// Missing fields, unsupported versions and malformed replies cannot start a package successfully.
func healthReady(raw []byte) bool {
	var response contracts.Envelope[struct {
		Status     string `json:"status"`
		Ready      *bool  `json:"ready"`
		ReasonCode string `json:"reasonCode"`
	}]
	return security.DecodeJSON(bytes.NewReader(raw), 4096, &response) == nil &&
		response.ValidateVersion() == nil && response.Data.Ready != nil &&
		*response.Data.Ready && response.Data.Status == "ready" && response.Data.ReasonCode == ""
}
