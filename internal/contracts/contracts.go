// Package contracts defines the versioned, lossless DiskMap wire model.
package contracts

import (
	"encoding/json"
	"fmt"
)

// Version identifies the API, CLI and DTO contract implemented here.
const Version = 1

// ValidScanMode preserves user intent; all V1 modes traverse the entire captured scope.
func ValidScanMode(mode string) bool {
	return mode == "full" || mode == "rebuild" || mode == "reconcile"
}

// ContractError exposes a stable machine-readable validation code.
type ContractError struct{ Code string }

// Error implements error without exposing source paths or private metadata.
func (e *ContractError) Error() string { return e.Code }

// Bytes is a canonical nonnegative decimal string; use *Bytes for unknown values.
type Bytes string

// Validate rejects signs, leading zeros, whitespace and exponent notation.
func (b Bytes) Validate() error {
	if len(b) == 0 || (len(b) > 1 && b[0] == '0') {
		return &ContractError{"INVALID_BYTES"}
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return &ContractError{"INVALID_BYTES"}
		}
	}
	return nil
}

// MarshalJSON always preserves byte counts as strings, never floating-point numbers.
func (b Bytes) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(b))
}

// UnmarshalJSON requires an explicit string; nullable fields use a nil pointer.
func (b *Bytes) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || data[0] != '"' {
		return &ContractError{"INVALID_BYTES"}
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return &ContractError{"INVALID_BYTES"}
	}
	next := Bytes(value)
	if err := next.Validate(); err != nil {
		return err
	}
	*b = next
	return nil
}

// Envelope is the common success envelope for API and CLI responses.
type Envelope[T any] struct {
	ContractVersion int    `json:"contractVersion"`
	RequestID       string `json:"requestId"`
	Data            T      `json:"data"`
}

// ValidateVersion fails closed for absent or unsupported contract versions.
func (e Envelope[T]) ValidateVersion() error {
	if e.ContractVersion != Version {
		return &ContractError{"CONTRACT_VERSION_UNSUPPORTED"}
	}
	return nil
}

// Measurement retains provenance and explicit unknown capacity values.
type Measurement struct {
	Value      *Bytes `json:"value"`
	Source     string `json:"source"`
	MeasuredAt string `json:"measuredAt"`
	Method     string `json:"method"`
}

// Volume identifies a verified mount independently from its display name.
type Volume struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	MountPath        string      `json:"mountPath"`
	IdentityEvidence string      `json:"identityEvidence"`
	Filesystem       *string     `json:"filesystem"`
	State            string      `json:"state"`
	Capacity         Measurement `json:"capacity"`
	Used             Measurement `json:"used"`
	Available        Measurement `json:"available"`
}

// Share records physical and File Station mappings separately.
type Share struct {
	// Unknown encryption state remains null until confirmed by DSM metadata.
	Encrypted        *bool   `json:"encrypted"`
	Locked           *bool   `json:"locked"`
	IdentityEvidence string  `json:"identityEvidence"`
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	VolumeID         string  `json:"volumeId"`
	PhysicalPath     string  `json:"physicalPath"`
	FileStationPath  *string `json:"fileStationPath"`
	State            string  `json:"state"`
	Readable         bool    `json:"readable"`
}

// Scope selects stable inventory identities rather than arbitrary client paths.
// Kinds nas, volumes, shares and mixed are normalized into disjoint roots before scanning.
type Scope struct {
	Kind         string   `json:"kind"`
	TargetIDs    []string `json:"targetIds"`
	RelativePath string   `json:"relativePath,omitempty"`
}

// Exclusion describes an explicit profile exclusion rule.
type Exclusion struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ResourceLimits defines bounded worker, throughput and CPU settings.
type ResourceLimits struct {
	Workers          int    `json:"workers"`
	IOBytesPerSecond *Bytes `json:"ioBytesPerSecond"`
	CPUPercent       *int   `json:"cpuPercent"`
}

// Profile is versioned so a running job retains its original configuration.
type Profile struct {
	ID               string         `json:"id"`
	Revision         int            `json:"revision"`
	Name             string         `json:"name"`
	Scope            Scope          `json:"scope"`
	Active           bool           `json:"active"`
	Exclusions       []Exclusion    `json:"exclusions"`
	ResourceMode     string         `json:"resourceMode"`
	Limits           ResourceLimits `json:"limits"`
	FreshnessSeconds int            `json:"freshnessSeconds"`
}

// APIError contains a redacted public error without raw filesystem details.
type APIError struct {
	Code       string `json:"code"`
	MessageKey string `json:"messageKey"`
	RequestID  string `json:"requestId"`
	Retryable  bool   `json:"retryable"`
}

// Job records real progress and a captured profile, never timer-derived estimates.
type Job struct {
	ID               string     `json:"id"`
	ProfileID        string     `json:"profileId"`
	ProfileRevision  int        `json:"profileRevision"`
	CapturedProfile  Profile    `json:"capturedProfile"`
	Trigger          string     `json:"trigger"`
	State            string     `json:"state"`
	ScanMode         string     `json:"scanMode"`
	CreatedAt        string     `json:"createdAt"`
	StartedAt        *string    `json:"startedAt"`
	EndedAt          *string    `json:"endedAt"`
	EntriesVisited   string     `json:"entriesVisited"`
	BytesObserved    Bytes      `json:"bytesObserved"`
	Warnings         []APIError `json:"warnings"`
	ResultSnapshotID *string    `json:"resultSnapshotId"`
	ScannerVersion   string     `json:"scannerVersion"`
	SchemaVersion    int        `json:"schemaVersion"`
}

// ScopeRoot captures the disjoint source roots used by a snapshot.
type ScopeRoot struct {
	VolumeID string  `json:"volumeId"`
	ShareID  *string `json:"shareId"`
	RootPath string  `json:"rootPath"`
}

// Snapshot describes an immutable published generation and its measurement limits.
type Snapshot struct {
	ID              string      `json:"id"`
	JobID           string      `json:"jobId"`
	ProfileID       string      `json:"profileId"`
	ProfileRevision int         `json:"profileRevision"`
	ScopeManifest   []ScopeRoot `json:"scopeManifest"`
	StartedAt       string      `json:"startedAt"`
	EndedAt         string      `json:"endedAt"`
	PublishedAt     *string     `json:"publishedAt"`
	State           string      `json:"state"`
	LogicalBytes    Bytes       `json:"logicalBytes"`
	AllocatedBytes  *Bytes      `json:"allocatedBytes"`
	StorageBytes    Bytes       `json:"storageBytes"`
	Methods         []string    `json:"methods"`
	Coverage        []Coverage  `json:"coverage"`
	SchemaVersion   int         `json:"schemaVersion"`
}

// Node identity is scoped to exactly one snapshot, including its parent relation.
type Node struct {
	SnapshotID     string  `json:"snapshotId"`
	ID             string  `json:"id"`
	ParentID       *string `json:"parentId"`
	OriginalName   string  `json:"originalName"`
	Kind           string  `json:"kind"`
	VolumeID       string  `json:"volumeId"`
	ShareID        *string `json:"shareId"`
	RelativePath   string  `json:"relativePath"`
	LogicalBytes   *Bytes  `json:"logicalBytes"`
	AllocatedBytes *Bytes  `json:"allocatedBytes"`
	ModifiedAt     *string `json:"modifiedAt"`
	CreatedAt      *string `json:"createdAt"`
	OwnerID        *string `json:"ownerId"`
	LinkCount      *int    `json:"linkCount"`
	LinkGroupID    *string `json:"linkGroupId"`
}

// ValidateNodeType rejects unknown kinds and fabricated special-file payload sizes.
func ValidateNodeType(n Node) error {
	switch n.Kind {
	case "file", "directory":
		return nil
	case "symlink", "fifo", "socket", "blockDevice", "characterDevice":
		if n.LogicalBytes == nil && n.AllocatedBytes == nil {
			return nil
		}
	}
	return &ContractError{"NODE_KIND_INVALID"}
}

// ScanBoundary is a bounded gap or intentional-exclusion record, never an actionable source path.
// Counts enumerate unique stored records, not syscall events; missing areas have no invented size.
type ScanBoundary struct {
	TargetID     string  `json:"targetId"`
	NodeID       *string `json:"nodeId"`
	ReasonCode   string  `json:"reasonCode"`
	RelativePath string  `json:"relativePath"`
}

// PackageUsage separates package-owned file metadata from scanned source sums.
type PackageUsage struct {
	LogicalBytes   *Bytes `json:"logicalBytes"`
	AllocatedBytes *Bytes `json:"allocatedBytes"`
	MeasuredAt     string `json:"measuredAt"`
	State          string `json:"state"`
	Method         string `json:"method"`
}

// Coverage distinguishes unknown or inaccessible data from measured zero bytes.
type Coverage struct {
	SnapshotID        string   `json:"snapshotId"`
	RootID            string   `json:"rootId"`
	State             string   `json:"state"`
	KnownEntries      *string  `json:"knownEntries"`
	ReasonCode        *string  `json:"reasonCode"`
	MeasurementLimits []string `json:"measurementLimits"`
}

// UserPreference belongs to a DSM identity and does not contain authentication data.
type UserPreference struct {
	Workspace          *PreferenceWorkspace `json:"workspace"`
	Display            PreferenceDisplay    `json:"display"`
	Version            int                  `json:"version"`
	LastView           *PreferenceView      `json:"lastView"`
	DSMUserID          string               `json:"dsmUserId"`
	Revision           int                  `json:"revision"`
	Language           string               `json:"language"`
	Theme              string               `json:"theme"`
	PreferredProfileID *string              `json:"preferredProfileId"`
	Map                string               `json:"map"`
	Locale             *string              `json:"locale"`
	TimeZone           *string              `json:"timeZone"`
}

// Hierarchy is a complete hierarchy or a normalized, explicitly bounded subtree.
type Hierarchy struct {
	ContractVersion int    `json:"contractVersion"`
	SnapshotID      string `json:"snapshotId"`
	Nodes           []Node `json:"nodes"`
}

// ValidateHierarchy checks identity and iterative parent walks in linear time.
func ValidateHierarchy(h Hierarchy) error {
	if h.ContractVersion != Version {
		return &ContractError{"CONTRACT_VERSION_UNSUPPORTED"}
	}
	if h.SnapshotID == "" || h.Nodes == nil {
		return &ContractError{"INVALID_REQUEST"}
	}
	nodes := make(map[string]Node, len(h.Nodes))
	for _, n := range h.Nodes {
		if err := ValidateNodeType(n); err != nil {
			return err
		}
		if _, exists := nodes[n.ID]; n.ID == "" || exists {
			return &ContractError{"NODE_ID_INVALID"}
		}
		if n.SnapshotID != h.SnapshotID {
			return &ContractError{"SNAPSHOT_MISMATCH"}
		}
		if n.ParentID != nil && *n.ParentID == "" {
			return &ContractError{"PARENT_INVALID"}
		}
		for _, b := range []*Bytes{n.LogicalBytes, n.AllocatedBytes} {
			if b != nil {
				if err := b.Validate(); err != nil {
					return err
				}
			}
		}
		nodes[n.ID] = n
	}
	complete := make(map[string]bool, len(nodes))
	for id := range nodes {
		path := make(map[string]bool)
		for !complete[id] {
			if path[id] {
				return &ContractError{"HIERARCHY_CYCLE"}
			}
			path[id] = true
			n := nodes[id]
			if n.ParentID == nil {
				break
			}
			id = *n.ParentID
			if _, exists := nodes[id]; !exists {
				return &ContractError{"PARENT_MISSING"}
			}
		}
		for visited := range path {
			complete[visited] = true
		}
	}
	return nil
}

// DecodeEnvelope checks the boundary version while preserving typed JSON validation.
func DecodeEnvelope[T any](data []byte) (Envelope[T], error) {
	var envelope Envelope[T]
	if err := json.Unmarshal(data, &envelope); err != nil {
		return envelope, fmt.Errorf("INVALID_REQUEST: %w", err)
	}
	return envelope, envelope.ValidateVersion()
}
