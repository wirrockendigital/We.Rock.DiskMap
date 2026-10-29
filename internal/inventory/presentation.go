package inventory

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"time"
)

// PresentationLimit bounds the complete source picker, never a filesystem tree.
const PresentationLimit = 1 << 20

// ScopeRequestLimit accommodates all 1536 bounded opaque IDs without an implicit smaller limit.
const ScopeRequestLimit = 256 << 10

// PresentationVolume excludes private mount paths and identity evidence.
type PresentationVolume struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// PresentationShare retains only the verified parent identity and readability observation.
type PresentationShare struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	VolumeID     *string `json:"volumeId"`
	State        string  `json:"state"`
	Readable     bool    `json:"readable"`
	RecycleState string  `json:"recycleState,omitempty"`
}

// Presentation is a bounded current catalogue, not a promise about descendants.
type Presentation struct {
	Volumes    []PresentationVolume `json:"volumes"`
	Shares     []PresentationShare  `json:"shares"`
	ObservedAt string               `json:"observedAt"`
}

// ScopePreviewRequest accepts inventory identities only and cannot carry a source path.
type ScopePreviewRequest struct {
	ContractVersion int             `json:"contractVersion"`
	Scope           contracts.Scope `json:"scope"`
}

// Validate rejects malformed or excessive selections before inventory work begins.
func (r ScopePreviewRequest) Validate() error {
	if r.ContractVersion != 1 || len(r.Scope.TargetIDs) > 1536 {
		return security.ErrInvalidRequest
	}
	if !contracts.ValidFolderScope(r.Scope) {
		return security.ErrInvalidRequest
	}
	if r.Scope.Kind != "folder" && r.Scope.Kind != "nas" && r.Scope.Kind != "volumes" && r.Scope.Kind != "shares" && r.Scope.Kind != "mixed" {
		return security.ErrInvalidRequest
	}
	if (r.Scope.Kind == "nas") != (len(r.Scope.TargetIDs) == 0) {
		return security.ErrInvalidRequest
	}
	seen := map[string]bool{}
	for _, id := range r.Scope.TargetIDs {
		if !security.ValidID(id) || seen[id] {
			return security.ErrInvalidRequest
		}
		seen[id] = true
	}
	return nil
}

// PreviewRoot intentionally omits every private descriptor and filesystem binding.
type PreviewRoot struct {
	TargetID string  `json:"targetId"`
	Kind     string  `json:"kind"`
	VolumeID *string `json:"volumeId"`
	State    string  `json:"state"`
}

// ScopePreview records only a root-level observation; scanner preflight must run again.
type ScopePreview struct {
	Scope            contracts.Scope `json:"scope"`
	CheckedAt        string          `json:"checkedAt"`
	Roots            []PreviewRoot   `json:"roots"`
	CoveredTargetIDs []string        `json:"coveredTargetIds"`
	Issues           []ScopeIssue    `json:"issues"`
}

// ValidPresentation defends the CGI boundary against malformed service responses.
func ValidPresentation(p Presentation) bool {
	if p.Volumes == nil || p.Shares == nil || len(p.Volumes) > 512 || len(p.Shares) > 1024 {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, p.ObservedAt); e != nil {
		return false
	}
	seen := map[string]bool{}
	volumes := map[string]bool{}
	for _, v := range p.Volumes {
		if !security.ValidID(v.ID) || seen[v.ID] || len(v.Name) > 4096 || !scopeState(v.State) {
			return false
		}
		seen[v.ID] = true
		volumes[v.ID] = true
	}
	for _, s := range p.Shares {
		// An omitted optional observation is unknown, never proof of absence.
		if s.RecycleState != "" && s.RecycleState != "unknown" && s.RecycleState != "readable" && s.RecycleState != "denied" && s.RecycleState != "missing" {
			return false
		}
		if !security.ValidID(s.ID) || seen[s.ID] || len(s.Name) > 4096 || !scopeState(s.State) || s.VolumeID != nil && !volumes[*s.VolumeID] || s.State == "reachable" && (!s.Readable || s.VolumeID == nil) {
			return false
		}
		seen[s.ID] = true
	}
	return true
}

// ValidPreview validates bounded returned identities without claiming current source authority.
func ValidPreview(p ScopePreview) bool {
	if (ScopePreviewRequest{ContractVersion: 1, Scope: p.Scope}).Validate() != nil || p.Roots == nil || len(p.Roots) > 1536 || p.CoveredTargetIDs == nil || len(p.CoveredTargetIDs) > 1536 || p.Issues == nil || len(p.Issues) > 3072 {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, p.CheckedAt); e != nil {
		return false
	}
	covered := map[string]bool{}
	for _, id := range p.CoveredTargetIDs {
		if !security.ValidID(id) || covered[id] {
			return false
		}
		covered[id] = true
	}
	seen := map[string]bool{}
	for _, r := range p.Roots {
		if !security.ValidID(r.TargetID) || seen[r.TargetID] || !covered[r.TargetID] || !scopeState(r.State) || r.Kind != "share" || r.VolumeID != nil && !security.ValidID(*r.VolumeID) {
			return false
		}
		// Native scopes always scan authorized shares; volumes remain navigation containers.
		if r.State == "reachable" && r.VolumeID == nil {
			return false
		}
		seen[r.TargetID] = true
	}
	for _, i := range p.Issues {
		if !covered[i.TargetID] || !scopeState(i.State) || !validScopeIssueCode(i.Code) {
			return false
		}
	}
	return true
}

// Only fixed diagnostic codes cross the boundary, never backend error strings.
func validScopeIssueCode(code string) bool {
	switch code {
	case "SCOPE_TARGET_UNMOUNTED", "SCOPE_TARGET_LOCKED", "SCOPE_TARGET_UNREADABLE", "SCOPE_TARGET_MISSING", "SCOPE_TARGET_UNVERIFIED", "SCOPE_BINDING_UNAVAILABLE", "SCOPE_BINDING_MISSING", "SCOPE_BINDING_UNREADABLE", "SCOPE_BINDING_CHANGED":
		return true
	}
	return false
}
