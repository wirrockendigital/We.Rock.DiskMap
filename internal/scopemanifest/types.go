package scopemanifest

import "context"

// ScopeManifestVersion versions the source-selection contract, not the index schema.
const ScopeManifestVersion = 2

// NormalizedRoot records source identity and kind without inferring them from a path.
type NormalizedRoot struct {
	TargetID     string  `json:"targetId"`
	Kind         string  `json:"kind"`
	VolumeID     string  `json:"volumeId"`
	ShareID      *string `json:"shareId"`
	PhysicalPath string  `json:"physicalPath"`
	RelativePath string  `json:"relativePath,omitempty"`
	State        string  `json:"state"`
}

// ScopeIssue retains unavailable selected or covered identities, including locked shares.
type ScopeIssue struct {
	TargetID string `json:"targetId"`
	Code     string `json:"code"`
	State    string `json:"state"`
}

// NormalizedScope contains disjoint roots and every identity those roots cover.
// Its exported values are snapshots; callers must treat them as immutable.
type NormalizedScope struct {
	ContractVersion  int              `json:"contractVersion"`
	Kind             string           `json:"kind"`
	Roots            []NormalizedRoot `json:"roots"`
	CoveredTargetIDs []string         `json:"coveredTargetIds"`
	Issues           []ScopeIssue     `json:"issues"`
}

// RootIdentity pins a verified directory without coupling inventory to the scanner.
type RootIdentity struct {
	Path    string `json:"path"`
	Device  string `json:"device"`
	Inode   string `json:"inode"`
	MountID string `json:"mountId"`
}

// ManifestRoot retains unavailable roots with a nil binding rather than silently dropping them.
type ManifestRoot struct {
	NormalizedRoot
	Binding *RootIdentity `json:"binding"`
}

// ScopeManifest is a detached run-start capture; binding failures remain explicit.
// FixedExclusion records an internal mandatory source boundary, never user input.
type FixedExclusion struct {
	TargetID     string `json:"targetId"`
	RelativePath string `json:"relativePath"`
	Code         string `json:"code"`
}

// ScopeManifest captures source identities and mandatory internal boundaries.
// QualifiedSubvolume permits a native registry share below a captured volume root.
// Its binding is private and never accepted from browser input.
type QualifiedSubvolume struct {
	TargetID     string       `json:"targetId"`
	SourceID     string       `json:"sourceId"`
	RelativePath string       `json:"relativePath"`
	Binding      RootIdentity `json:"binding"`
	TreeID       string       `json:"treeId"`
}

type ScopeManifest struct {
	Provenance          SourceProvenance     `json:"provenance"`
	QualifiedSubvolumes []QualifiedSubvolume `json:"qualifiedSubvolumes"`
	Exclusions          []FixedExclusion     `json:"exclusions"`
	ContractVersion     int                  `json:"contractVersion"`
	Kind                string               `json:"kind"`
	Roots               []ManifestRoot       `json:"roots"`
	CoveredTargetIDs    []string             `json:"coveredTargetIds"`
	Issues              []ScopeIssue         `json:"issues"`
}

// BindRoot must verify directory identity and the supplied authoritative volume identity.
// It must not enumerate contents or infer a different source from a display name.
type BindRoot func(context.Context, NormalizedRoot) (RootIdentity, error)
