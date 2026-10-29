package main

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"testing"
)

// The service's actual metadata envelope must survive the strict CGI response decoder.
func TestScopeObservationResponseRoundTrip(t *testing.T) {
	input := contracts.Envelope[[]jobs.ScopeObservation]{ContractVersion: 1, Data: []jobs.ScopeObservation{{ShareID: "share-1", Path: ".", State: "unscanned", Reasons: []contracts.ScanBoundary{}}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output contracts.Envelope[[]jobs.ScopeObservation]
	if err = security.DecodeJSON(bytes.NewReader(raw), 1<<20, &output); err != nil {
		t.Fatalf("service response rejected: %v", err)
	}
	if len(output.Data) != 1 || output.Data[0].ShareID != "share-1" || output.Data[0].Path != "." {
		t.Fatal("scope identity lost")
	}
}
