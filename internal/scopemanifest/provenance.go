package scopemanifest

// SourceProvenance is mandatory immutable source authority for generation schema 10.
// Relative paths are raw bytes; JSON therefore preserves them as base64, never lossy Unicode.
type SourceProvenance struct {
	Version   int          `json:"version"`
	Authority string       `json:"authority"`
	Roots     []SourceRoot `json:"roots"`
}

// SourceRoot binds a captured source catalog to one disjoint traversal root.
type SourceRoot struct {
	TargetID   string        `json:"targetId"`
	VolumeID   string        `json:"volumeId"`
	VolumeName string        `json:"volumeName"`
	SourceID   *string       `json:"sourceId"`
	Name       string        `json:"name"`
	Kind       string        `json:"kind"`
	Binding    *RootIdentity `json:"binding"`
	Areas      []SourceArea  `json:"areas"`
}

// SourceArea records catalog identity independently of access and measured coverage.
// An unavailable mapping grants no traversal authority and must never imply zero content.
type SourceArea struct {
	SourceID      *string       `json:"sourceId"`
	VolumeID      string        `json:"volumeId"`
	Name          string        `json:"name"`
	RelativePath  []byte        `json:"relativePath"`
	Binding       *RootIdentity `json:"binding"`
	State         string        `json:"state"`
	Qualification string        `json:"qualification"`
}

// ExplicitProvenance records only declared roots; it does not invent a native share catalog.
// Synthetic fixtures must explicitly choose their separate authority before sealing.
func ExplicitProvenance(manifest ScopeManifest, synthetic bool) SourceProvenance {
	p := SourceProvenance{Version: 1, Authority: "explicit-roots", Roots: []SourceRoot{}}
	if synthetic {
		p.Authority = "synthetic-fixture"
	}
	for _, r := range manifest.Roots {
		id := r.TargetID
		root := SourceRoot{TargetID: r.TargetID, VolumeID: r.VolumeID, VolumeName: r.VolumeID, SourceID: &id, Name: r.TargetID, Kind: r.Kind, Areas: []SourceArea{}}
		if r.Binding != nil {
			b := *r.Binding
			root.Binding = &b
		}
		p.Roots = append(p.Roots, root)
	}
	return p
}
