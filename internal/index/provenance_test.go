package index

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// sourceManifest creates independent captured authority, including a non-Unicode raw boundary.
func sourceManifest() scopemanifest.ScopeManifest {
	id := "share"
	b := scopemanifest.RootIdentity{Path: "/volume/share", Device: "1", Inode: "2", MountID: "mount"}
	m := scopemanifest.ScopeManifest{Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume", PhysicalPath: "/volume"}, Binding: &scopemanifest.RootIdentity{Path: "/volume", Device: "1", Inode: "1", MountID: "mount"}}}, CoveredTargetIDs: []string{id}}
	m.Provenance = scopemanifest.ExplicitProvenance(m, false)
	m.Provenance.Authority = "native-inventory"
	m.Provenance.Roots[0].Areas = []scopemanifest.SourceArea{{SourceID: &id, VolumeID: "volume", Name: "Historic share", RelativePath: []byte("share"), Binding: &b, State: "reachable", Qualification: "qualified"}}
	return m
}

// TestSourceProvenanceRejectsAmbiguity checks authority without touching source paths.
func TestSourceProvenanceRejectsAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*scopemanifest.ScopeManifest)
	}{
		{"missing", func(m *scopemanifest.ScopeManifest) { m.Provenance = scopemanifest.SourceProvenance{} }},
		{"root-binding", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Binding = nil }},
		{"foreign-volume", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Areas[0].VolumeID = "foreign" }},
		{"foreign-share", func(m *scopemanifest.ScopeManifest) { s := "foreign"; m.Provenance.Roots[0].Areas[0].SourceID = &s }},
		{"escape", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Areas[0].RelativePath = []byte("../share") }},
		{"path-binding", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Areas[0].Binding.Path = "/elsewhere" }},
		{"foreign-mount", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Areas[0].Binding.MountID = "elsewhere" }},
		{"duplicate", func(m *scopemanifest.ScopeManifest) {
			m.Provenance.Roots[0].Areas = append(m.Provenance.Roots[0].Areas, m.Provenance.Roots[0].Areas[0])
		}},
		{"invented-native", func(m *scopemanifest.ScopeManifest) { m.Provenance.Authority = "explicit-roots" }},
		{"unknown-qualified", func(m *scopemanifest.ScopeManifest) { m.Provenance.Roots[0].Areas[0].SourceID = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := sourceManifest()
			tc.change(&m)
			if ValidateSourceProvenance(m) == nil {
				t.Fatal("invalid source authority accepted")
			}
		})
	}
	m := sourceManifest()
	if ValidateSourceProvenance(m) != nil {
		t.Fatal("valid mapping rejected")
	}
	// Raw-byte identity survives JSON without replacement characters becoming authoritative.
	a := &m.Provenance.Roots[0].Areas[0]
	a.RelativePath = []byte{'s', 0xff}
	a.Binding.Path = "/volume/s\xff"
	raw, _ := json.Marshal(m)
	var restored scopemanifest.ScopeManifest
	if json.Unmarshal(raw, &restored) != nil {
		t.Fatal("roundtrip")
	}
	// Binding.Path is textual, so invalid native root paths must not silently qualify after JSON.
	if ValidateSourceProvenance(restored) == nil {
		t.Fatal("lossy physical binding accepted")
	}
	a.Binding = nil
	a.Qualification = "unavailable"
	a.State = "unverified"
	raw, _ = json.Marshal(m)
	if json.Unmarshal(raw, &restored) != nil || ValidateSourceProvenance(restored) != nil || string(restored.Provenance.Roots[0].Areas[0].RelativePath) != "s\xff" {
		t.Fatal("raw unavailable identity lost")
	}
}

// TestGenerationRequiresProvenance leaves the working generation unsealed on missing authority.
func TestGenerationRequiresProvenance(t *testing.T) {
	db, h := generationFixture(t)
	h.Manifest.Provenance = scopemanifest.SourceProvenance{}
	if FinalizeGeneration(context.Background(), db, h) == nil {
		t.Fatal("missing mandatory provenance accepted")
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM generation_header`).Scan(&n); e != nil || n != 0 {
		t.Fatal("failed seal published", e, n)
	}
}

// TestProvenanceBoundaryRequiresMeasuredDirectoryOrGap forbids silently invented or replaced share roots.
func TestProvenanceBoundaryRequiresMeasuredDirectoryOrGap(t *testing.T) {
	db, h := generationFixture(t)
	m := sourceManifest()
	h.Manifest = m
	if _, e := db.Exec(`UPDATE nodes SET object_identity='1:1:0:0' WHERE parent_id IS NULL`); e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if validateProvenanceRows(context.Background(), tx, h) == nil {
		t.Fatal("missing boundary without gap accepted")
	}
	if _, e = tx.Exec(`INSERT INTO scan_boundaries VALUES('volume',?,'SOURCE_UNREADABLE')`, []byte("share")); e != nil {
		t.Fatal(e)
	}
	if e = validateProvenanceRows(context.Background(), tx, h); e != nil {
		t.Fatal("explicit gap rejected", e)
	}
	a := &h.Manifest.Provenance.Roots[0].Areas[0]
	a.RelativePath = []byte("large")
	a.Binding.Path = "/volume/large"
	if validateProvenanceRows(context.Background(), tx, h) == nil {
		t.Fatal("regular file masqueraded as share root")
	}
	tx.Rollback()
}
