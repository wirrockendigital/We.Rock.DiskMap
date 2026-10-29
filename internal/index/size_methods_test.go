package index

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"testing"
)

// TestSizeMethodsSeal preserves logical-only historical headers and admits only the exact new marker.
func TestSizeMethodsSeal(t *testing.T) {
	for _, methods := range [][]string{{"descriptor-no-follow-stat"}, {"descriptor-no-follow-stat", contracts.AllocatedQualification}} {
		if !validGenerationMethods(methods) {
			t.Fatal("valid methods rejected", methods)
		}
	}
	for _, methods := range [][]string{nil, {contracts.AllocatedQualification}, {"descriptor-no-follow-stat", "unknown"}, {"descriptor-no-follow-stat", contracts.AllocatedQualification, contracts.AllocatedQualification}} {
		if validGenerationMethods(methods) {
			t.Fatal("unqualified methods accepted", methods)
		}
	}
	// Validate the new marker through the full real seal, not just its standalone allowlist.
	db, h := generationFixture(t)
	h.Methods = append(h.Methods, contracts.AllocatedQualification)
	if err := FinalizeGeneration(context.Background(), db, h); err != nil {
		t.Fatal(err)
	}
}
