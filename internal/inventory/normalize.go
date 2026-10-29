package inventory

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// ErrRootChanged reports a verified identity or mapping change without exposing a source path.
var ErrRootChanged = errors.New("ROOT_CHANGED")

// Manifest types are shared without introducing a scanner/inventory import cycle.
const ScopeManifestVersion = scopemanifest.ScopeManifestVersion

type NormalizedRoot = scopemanifest.NormalizedRoot
type ScopeIssue = scopemanifest.ScopeIssue
type NormalizedScope = scopemanifest.NormalizedScope
type RootIdentity = scopemanifest.RootIdentity
type ManifestRoot = scopemanifest.ManifestRoot
type ScopeManifest = scopemanifest.ScopeManifest
type BindRoot = scopemanifest.BindRoot

// Normalize uses only current inventory metadata; a share-only scope never probes a volume.
func Normalize(scope contracts.Scope, volumes []contracts.Volume, shares []contracts.Share) (NormalizedScope, error) {
	fail := func(code string) (NormalizedScope, error) { return NormalizedScope{}, errors.New(code) }
	if len(volumes) > 1024 || len(shares) > 1024 {
		return fail("SCOPE_LIMIT_EXCEEDED")
	}
	known := map[string]NormalizedRoot{}
	byVolume := map[string]contracts.Volume{}
	for _, v := range volumes {
		if v.ID == "" || known[v.ID].TargetID != "" || !scopePath(v.MountPath) || !scopeState(v.State) {
			return fail("INVALID_SCOPE_INVENTORY")
		}
		known[v.ID] = NormalizedRoot{TargetID: v.ID, Kind: "volume", VolumeID: v.ID, PhysicalPath: v.MountPath, State: v.State}
		byVolume[v.ID] = v
	}
	for _, s := range shares {
		if s.ID == "" || known[s.ID].TargetID != "" || !scopePath(s.PhysicalPath) || !scopeState(s.State) {
			return fail("INVALID_SCOPE_INVENTORY")
		}
		v, ok := byVolume[s.VolumeID]
		// Unverified unavailable shares may lack a volume identity. They can never be bound.
		if s.State == "reachable" && (!ok || !s.Readable || v.State != "reachable") {
			return fail("INVALID_SCOPE_SHARE_BINDING")
		}
		if s.VolumeID != "" && (!ok || !scopeDescendant(v.MountPath, s.PhysicalPath)) {
			return fail("INVALID_SCOPE_SHARE_BINDING")
		}
		if s.Locked != nil && *s.Locked && s.State != "locked" {
			return fail("INVALID_SCOPE_SHARE_BINDING")
		}
		id := s.ID
		known[s.ID] = NormalizedRoot{TargetID: s.ID, Kind: "share", VolumeID: s.VolumeID, ShareID: &id, PhysicalPath: s.PhysicalPath, State: s.State}
	}

	qualified := make([]NormalizedRoot, 0, len(known))
	for _, r := range known {
		qualified = append(qualified, r)
	}
	normalized, err := NormalizeQualified(scope, qualified)
	if err != nil {
		return NormalizedScope{}, err
	}
	// Native volumes are navigation containers, never permission-expanding scan roots.
	roots := make([]NormalizedRoot, 0, len(shares))
	for _, root := range normalized.Roots {
		if root.Kind != "volume" {
			roots = append(roots, root)
			continue
		}
		for _, share := range shares {
			if share.VolumeID == root.VolumeID {
				roots = append(roots, cloneScopeRoot(known[share.ID]))
			}
		}
	}
	if len(roots) == 0 {
		return fail("EMPTY_SCOPE")
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].TargetID < roots[j].TargetID })
	normalized.Roots = roots
	return normalized, nil
}

// NormalizeQualified is the common disjoint-root core for native inventory and
// explicitly privileged isolated fixtures. Callers establish identity authority first.
func NormalizeQualified(scope contracts.Scope, qualified []NormalizedRoot) (NormalizedScope, error) {
	result := NormalizedScope{ContractVersion: ScopeManifestVersion, Kind: scope.Kind, Roots: []NormalizedRoot{}, CoveredTargetIDs: []string{}, Issues: []ScopeIssue{}}
	fail := func(code string) (NormalizedScope, error) { return NormalizedScope{}, errors.New(code) }
	if len(qualified) > 2048 || len(scope.TargetIDs) > 2048 {
		return fail("SCOPE_LIMIT_EXCEEDED")
	}
	if !contracts.ValidFolderScope(scope) {
		return fail("INVALID_SCOPE_PATH")
	}
	if scope.Kind != "folder" && scope.Kind != "nas" && scope.Kind != "volumes" && scope.Kind != "shares" && scope.Kind != "mixed" {
		return fail("INVALID_SCOPE_KIND")
	}
	if (scope.Kind == "nas") != (len(scope.TargetIDs) == 0) {
		return fail("INVALID_SCOPE_TARGETS")
	}
	known := map[string]NormalizedRoot{}
	byVolume := map[string]NormalizedRoot{}
	for _, r := range qualified {
		if r.TargetID == "" || known[r.TargetID].TargetID != "" || !scopePath(r.PhysicalPath) || !scopeState(r.State) {
			return fail("INVALID_SCOPE_INVENTORY")
		}
		switch r.Kind {
		case "volume":
			if r.VolumeID != r.TargetID || r.ShareID != nil {
				return fail("INVALID_SCOPE_INVENTORY")
			}
			byVolume[r.TargetID] = r
		case "share":
			if r.ShareID == nil || *r.ShareID != r.TargetID || r.State == "reachable" && r.VolumeID == "" {
				return fail("INVALID_SCOPE_INVENTORY")
			}
		default:
			return fail("INVALID_SCOPE_INVENTORY")
		}
		known[r.TargetID] = cloneScopeRoot(r)
	}
	for _, r := range known {
		if v, ok := byVolume[r.VolumeID]; r.Kind == "share" && ok && !scopeDescendant(v.PhysicalPath, r.PhysicalPath) {
			return fail("INVALID_SCOPE_SHARE_BINDING")
		}
	}
	selected := map[string]bool{}
	if scope.Kind == "nas" {
		for id := range byVolume {
			selected[id] = true
		}
	} else {
		for _, id := range scope.TargetIDs {
			r, ok := known[id]
			if !ok {
				return fail("UNKNOWN_SCOPE_TARGET")
			}
			if scope.Kind == "volumes" && r.Kind != "volume" || (scope.Kind == "shares" || scope.Kind == "folder") && r.Kind != "share" {
				return fail("INVALID_SCOPE_TARGET_KIND")
			}
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return fail("EMPTY_SCOPE")
	}
	covered := map[string]bool{}
	for id := range selected {
		covered[id] = true
	}
	// Coverage follows the native VolumeID relationship, never a name or prefix guess.
	for id, r := range known {
		if r.Kind == "share" && (selected[r.VolumeID] || scope.Kind == "nas") {
			covered[id] = true
		}
	}
	for id := range selected {
		r := known[id]
		if r.Kind == "share" && selected[r.VolumeID] {
			continue
		}
		// A folder retains its authoritative share identity but captures only the selected subtree.
		if scope.Kind == "folder" {
			r.RelativePath = scope.RelativePath
			r.PhysicalPath = path.Join(r.PhysicalPath, scope.RelativePath)
		}
		result.Roots = append(result.Roots, cloneScopeRoot(r))
	}
	sort.Slice(result.Roots, func(i, j int) bool { return result.Roots[i].TargetID < result.Roots[j].TargetID })
	for i, a := range result.Roots {
		for _, b := range result.Roots[i+1:] {
			if a.State == "reachable" && b.State == "reachable" && (a.PhysicalPath == b.PhysicalPath || scopeDescendant(a.PhysicalPath, b.PhysicalPath) || scopeDescendant(b.PhysicalPath, a.PhysicalPath)) {
				return fail("AMBIGUOUS_SCOPE_ROOTS")
			}
		}
	}
	for id := range covered {
		result.CoveredTargetIDs = append(result.CoveredTargetIDs, id)
		r := known[id]
		if r.State != "reachable" {
			result.Issues = append(result.Issues, ScopeIssue{TargetID: id, Code: "SCOPE_TARGET_" + strings.ToUpper(r.State), State: r.State})
		}
	}
	sort.Strings(result.CoveredTargetIDs)
	sortScopeIssues(result.Issues)
	return result, nil
}

// FreezeScope binds only reachable normalized roots and copies all mutable containers.
// Errors from a binder become unavailable manifest entries; cancellation aborts the capture.
func FreezeScope(ctx context.Context, normalized NormalizedScope, bind BindRoot) (ScopeManifest, error) {
	if !validNormalizedScope(normalized) || bind == nil {
		return ScopeManifest{}, errors.New("INVALID_NORMALIZED_SCOPE")
	}
	manifest := ScopeManifest{ContractVersion: ScopeManifestVersion, Kind: normalized.Kind, Roots: []ManifestRoot{}, CoveredTargetIDs: append([]string{}, normalized.CoveredTargetIDs...), Issues: append([]ScopeIssue{}, normalized.Issues...)}
	for _, r := range normalized.Roots {
		if err := ctx.Err(); err != nil {
			return ScopeManifest{}, err
		}
		root := ManifestRoot{NormalizedRoot: cloneScopeRoot(r)}
		if r.State == "reachable" {
			identity, err := bind(ctx, cloneScopeRoot(r))
			if ctx.Err() != nil {
				return ScopeManifest{}, ctx.Err()
			}
			if err != nil || identity.Path != r.PhysicalPath || identity.Device == "" || identity.Inode == "" || identity.MountID == "" {
				root.State = "unverified"
				code := "SCOPE_BINDING_UNAVAILABLE"
				// Preserve known metadata failures; only unclassified observations remain unverified.
				switch {
				case errors.Is(err, fs.ErrNotExist):
					root.State, code = "missing", "SCOPE_BINDING_MISSING"
				case errors.Is(err, fs.ErrPermission):
					root.State, code = "unreadable", "SCOPE_BINDING_UNREADABLE"
				case errors.Is(err, ErrRootChanged):
					code = "SCOPE_BINDING_CHANGED"
				}
				manifest.Issues = append(manifest.Issues, ScopeIssue{TargetID: r.TargetID, Code: code, State: root.State})
			} else {
				root.Binding = &identity
			}
		}
		manifest.Roots = append(manifest.Roots, root)
	}
	sortScopeIssues(manifest.Issues)
	manifest.Provenance = scopemanifest.ExplicitProvenance(manifest, false)
	return manifest, nil
}

// cloneScopeRoot detaches optional identity pointers from caller-owned inventory values.
func cloneScopeRoot(r NormalizedRoot) NormalizedRoot {
	if r.ShareID != nil {
		id := *r.ShareID
		r.ShareID = &id
	}
	return r
}

// scopePath accepts canonical absolute source paths without NUL bytes.
func scopePath(value string) bool {
	return value != "/" && path.IsAbs(value) && path.Clean(value) == value && !strings.ContainsRune(value, 0)
}

// scopeDescendant compares complete path components after canonical validation.
func scopeDescendant(parent, child string) bool { return strings.HasPrefix(child, parent+"/") }

// scopeState rejects inventory states not covered by the source contract.
func scopeState(state string) bool {
	switch state {
	case "reachable", "unmounted", "locked", "unreadable", "missing", "unverified":
		return true
	}
	return false
}

// sortScopeIssues makes failure coverage independent of map iteration order.
func sortScopeIssues(issues []ScopeIssue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].TargetID == issues[j].TargetID {
			return issues[i].Code < issues[j].Code
		}
		return issues[i].TargetID < issues[j].TargetID
	})
}

// validNormalizedScope defends the binding boundary against malformed or modified value objects.
// Inventory authority still belongs to Normalize; this is not a client-submittable DTO.
func validNormalizedScope(n NormalizedScope) bool {
	if n.ContractVersion != ScopeManifestVersion || len(n.Roots) == 0 || len(n.Roots) > 2048 || len(n.CoveredTargetIDs) > 2048 {
		return false
	}
	if n.Kind != "folder" && n.Kind != "nas" && n.Kind != "volumes" && n.Kind != "shares" && n.Kind != "mixed" {
		return false
	}
	covered := map[string]bool{}
	for _, id := range n.CoveredTargetIDs {
		if id == "" || covered[id] {
			return false
		}
		covered[id] = true
	}
	seen := map[string]bool{}
	for i, r := range n.Roots {
		if r.TargetID == "" || seen[r.TargetID] || !covered[r.TargetID] || !scopePath(r.PhysicalPath) || !scopeState(r.State) {
			return false
		}
		seen[r.TargetID] = true
		if (n.Kind == "folder" && (len(n.Roots) != 1 || !contracts.ValidFolderPath(r.RelativePath, false))) || (n.Kind != "folder" && r.RelativePath != "") {
			return false
		}
		switch r.Kind {
		case "volume":
			if r.VolumeID != r.TargetID || r.ShareID != nil || n.Kind == "shares" || n.Kind == "folder" {
				return false
			}
		case "share":
			if r.ShareID == nil || *r.ShareID != r.TargetID || r.State == "reachable" && r.VolumeID == "" {
				return false
			}
		default:
			return false
		}
		for _, other := range n.Roots[i+1:] {
			if r.State == "reachable" && other.State == "reachable" && (r.PhysicalPath == other.PhysicalPath || scopeDescendant(r.PhysicalPath, other.PhysicalPath) || scopeDescendant(other.PhysicalPath, r.PhysicalPath)) {
				return false
			}
		}
	}
	return true
}
