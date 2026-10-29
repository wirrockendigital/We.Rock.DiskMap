package service

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"strings"
	"testing"
)

// TestPublicOutcomeOmitsPrivateProvenance checks the actual submission/read serialization and alias safety.
func TestPublicOutcomeOmitsPrivateProvenance(t *testing.T) {
	manifest := scopemanifest.ScopeManifest{ContractVersion: 2, Roots: []scopemanifest.ManifestRoot{}, Provenance: scopemanifest.SourceProvenance{Version: 1, Authority: "native-inventory", Roots: []scopemanifest.SourceRoot{{Name: strings.Repeat("x", 100000), Binding: &scopemanifest.RootIdentity{Path: "private-new-boundary"}}}}}
	out := jobs.Outcome{ScopeManifest: &manifest, Job: contracts.Job{EntriesVisited: "0", BytesObserved: "0"}}
	stored, _ := json.Marshal(out)
	for _, value := range []any{publicOutcome(out), publicSubmission(jobs.Submission{Outcome: out, Deduplicated: true})} {
		raw, e := json.Marshal(value)
		if e != nil || len(raw) > 64*1024 || strings.Contains(string(raw), "provenance") || strings.Contains(string(raw), "private-new-boundary") {
			t.Fatal("private provenance leaked", e, len(raw))
		}
		var decoded jobs.Outcome
		if json.Unmarshal(raw, &decoded) != nil || decoded.ScopeManifest == nil || decoded.ScopeManifest.ContractVersion != 2 {
			t.Fatal("job client contract changed")
		}
	}
	after, _ := json.Marshal(out)
	if string(stored) != string(after) || !strings.Contains(string(after), "private-new-boundary") {
		t.Fatal("persistent outcome mutated")
	}
}
