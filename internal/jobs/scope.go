package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// profileSources explicitly distinguishes native identity authority from isolated test fixtures.
// No missing-mode fallback or synthetic volume mount path is accepted.
type profileSources struct {
	EncryptedMounts   map[string]inventory.EncryptedMount `json:"encryptedMounts,omitempty"`
	BindingMounts     map[string]inventory.EncryptedMount `json:"bindingMounts,omitempty"`
	BootID            string                              `json:"bootId,omitempty"`
	BindingIdentities map[string]string                   `json:"bindingIdentities,omitempty"`
	BindingBoots      map[string]string                   `json:"bindingBoots,omitempty"`
	Version           int                                 `json:"version"`
	Mode              string                              `json:"mode"`
	Qualified         []inventory.NormalizedRoot          `json:"qualified"`
	Volumes           []contracts.Volume                  `json:"volumes"`
	Shares            []contracts.Share                   `json:"shares"`
}

// nativeCatalog refreshes authoritative identities without enumerating source contents.
func nativeCatalog(ctx context.Context, previous profileSources) (profileSources, error) {
	boot, err := nativeBoot()
	if err != nil {
		return profileSources{}, err
	}
	v, err := inventory.Discover(ctx, previous.Volumes)
	if err != nil {
		return profileSources{}, err
	}
	s, err := inventory.DiscoverShares(ctx, v.Volumes, previous.Shares)
	if err != nil {
		return profileSources{}, err
	}
	endBoot, err := nativeBoot()
	if err != nil || endBoot != boot {
		return profileSources{}, ErrUnavailable
	}
	return profileSources{Version: 1, Mode: "native_inventory", BootID: boot, Volumes: inventory.VisibleVolumes(v.Volumes, s.Shares), Shares: s.Shares, EncryptedMounts: inventory.ObserveEncryptedMounts(ctx, v.Volumes, s.Shares)}, nil
}

// normalizeSources delegates all disjoint-root decisions to the single inventory core.
func normalizeSources(scope contracts.Scope, sources profileSources) (inventory.NormalizedScope, error) {
	if sources.Version != 1 {
		return inventory.NormalizedScope{}, errors.New("PROFILE_REIMPORT_REQUIRED")
	}
	switch sources.Mode {
	case "native_inventory":
		return inventory.Normalize(scope, sources.Volumes, sources.Shares)
	case "isolated_roots":
		return inventory.NormalizeQualified(scope, sources.Qualified)
	default:
		return inventory.NormalizedScope{}, errors.New("PROFILE_REIMPORT_REQUIRED")
	}
}

// catalogReader is the native metadata boundary; tests inject complete observations, not filesystem guesses.
type catalogReader func(context.Context, profileSources) (profileSources, error)

// scopeCapture separates immutable start evidence from the next persisted selection baseline.
type scopeCapture struct {
	Manifest inventory.ScopeManifest
	Sources  profileSources
	Bindings map[string]scanner.RootBinding
	Added    []string
}

// captureScope verifies known roots and binds only explicitly authorized first observations.
func captureScope(ctx context.Context, p contracts.Profile, previous profileSources, bindings map[string]scanner.RootBinding, state string, read catalogReader) (scopeCapture, error) {
	return captureScopeWithRecovery(ctx, p, previous, bindings, state, read, false)
}

// captureScopeWithRecovery keeps explicit baseline recovery separate from automatic boot transitions.
func captureScopeWithRecovery(ctx context.Context, p contracts.Profile, previous profileSources, bindings map[string]scanner.RootBinding, state string, read catalogReader, explicit bool) (scopeCapture, error) {
	captured := scopeCapture{Sources: previous, Bindings: map[string]scanner.RootBinding{}}
	for id, b := range bindings {
		captured.Bindings[id] = b
	}
	prior, err := normalizeSources(p.Scope, previous)
	if err != nil {
		return captured, err
	}
	priorRoots := map[string]inventory.NormalizedRoot{}
	for _, r := range prior.Roots {
		priorRoots[r.TargetID] = r
	}
	if previous.Mode == "native_inventory" {
		captured.Sources, err = read(ctx, previous)
		if err != nil {
			return captured, err
		}
		captured.Sources = retainScopeIdentities(previous, captured.Sources)
		captured.Sources.BindingMounts = copyMountProofs(previous.BindingMounts)
		captured.Sources.BindingBoots = bindingEpochs(previous.BindingBoots)
		captured.Sources.BindingIdentities = bindingEpochs(previous.BindingIdentities)
	}

	// Preserve legacy stable mapping evidence before any fresh observation can replace it.
	// This does not invent an old boot: an explicit recovery is still required.
	if previous.Mode == "native_inventory" {
		for _, r := range prior.Roots {
			if _, bound := bindings[r.TargetID]; bound && captured.Sources.BindingIdentities[r.TargetID] == "" {
				captured.Sources.BindingIdentities[r.TargetID] = nativeRootIdentity(previous, r)
			}
		}
	}
	n, err := normalizeSources(p.Scope, captured.Sources)
	if err != nil {
		return captured, err
	}
	if previous.Mode == "native_inventory" && p.Scope.Kind == "nas" {
		for _, r := range n.Roots {
			if _, known := priorRoots[r.TargetID]; !known {
				captured.Added = append(captured.Added, r.TargetID)
			}
		}
	}
	captured.Manifest, err = inventory.FreezeScope(ctx, n, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		if !safeSourcePath(r.PhysicalPath, state, previous.Mode == "isolated_roots") {
			return inventory.RootIdentity{}, ErrInvalid
		}
		old, bound := bindings[r.TargetID]
		expected, known := priorRoots[r.TargetID]
		// Known UUIDs with changed mappings must never be treated as newly authorized targets.
		if known && (expected.VolumeID != r.VolumeID || expected.PhysicalPath != r.PhysicalPath || expected.Kind != r.Kind) {
			return inventory.RootIdentity{}, inventory.ErrRootChanged
		}
		// Failed catalog observations never replace the identity attached to an existing binding.
		if signature := captured.Sources.BindingIdentities[r.TargetID]; signature != "" && signature != nativeRootIdentity(captured.Sources, r) {
			return inventory.RootIdentity{}, inventory.ErrRootChanged
		}
		firstNative := previous.Mode == "native_inventory" && !bound && known && (expected.State != "reachable" || p.Scope.Kind == "nas")
		addedNative := previous.Mode == "native_inventory" && !known && r.Kind == "share" && (p.Scope.Kind == "nas" || p.Scope.Kind == "volumes" || p.Scope.Kind == "mixed")
		if !bound && !firstNative && !addedNative {
			return inventory.RootIdentity{}, errors.New("ROOT_BINDING_UNAVAILABLE")
		}
		descriptor, binding, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return inventory.RootIdentity{}, e
		}
		descriptor.Close()
		if !mountAuthorityUnchanged(previous, captured.Sources, r, binding) {
			return inventory.RootIdentity{}, inventory.ErrRootChanged
		}
		rebind := mayRebind(previous, captured.Sources, r, explicit) || mayRemount(previous, captured.Sources, r, binding)
		if bound && old.Path != r.PhysicalPath {
			rebind = false
		}
		if bound && binding != old && !rebind {
			return inventory.RootIdentity{}, inventory.ErrRootChanged
		}
		if !bound || rebind {
			captured.Bindings[r.TargetID] = binding
			if validBoot(captured.Sources.BootID) {
				captured.Sources.BindingBoots[r.TargetID] = captured.Sources.BootID
				captured.Sources.BindingIdentities[r.TargetID] = nativeRootIdentity(captured.Sources, r)
			}
		}
		// Stamp only a descriptor-qualified observation; changed roots require prior authority.
		stampMountProof(&captured.Sources, r, binding)
		return inventory.RootIdentity{Path: binding.Path, Device: binding.Device, Inode: binding.Inode, MountID: binding.MountID}, nil
	})
	if err != nil {
		return captured, err
	}
	verified := map[string]scanner.RootBinding{}
	for _, r := range captured.Manifest.Roots {
		if r.Binding != nil {
			verified[r.TargetID] = captured.Bindings[r.TargetID]
		}
	}
	if err = confirmNativeBindings(ctx, p, captured.Sources, verified, read); err != nil {
		return captured, err
	}
	qualifySubvolumes(ctx, &captured.Manifest, captured.Sources)
	if err = captureSourceProvenance(ctx, &captured.Manifest, captured.Sources); err != nil {
		return captured, err
	}
	// A legacy failed capture must retain its only pre-provenance catalog baseline.
	// Availability remains expressed by the immutable manifest's issues, not a rewritten authority.
	if len(captured.Manifest.Issues) > 0 && previous.Mode == "native_inventory" {
		legacy := false
		for id := range bindings {
			if previous.BindingBoots[id] == "" {
				legacy = true
			}
		}
		if legacy {
			epochs, identities := captured.Sources.BindingBoots, captured.Sources.BindingIdentities
			captured.Sources = previous
			captured.Sources.BindingBoots = epochs
			captured.Sources.BindingIdentities = identities
		}
	}
	captured.Manifest.Exclusions = []scopemanifest.FixedExclusion{}
	for _, r := range captured.Manifest.Roots {
		if strings.HasPrefix(state, r.PhysicalPath+string(os.PathSeparator)) {
			relative, e := filepath.Rel(r.PhysicalPath, state)
			if e != nil {
				return captured, e
			}
			captured.Manifest.Exclusions = append(captured.Manifest.Exclusions, scopemanifest.FixedExclusion{TargetID: r.TargetID, RelativePath: filepath.ToSlash(relative), Code: "PACKAGE_STATE_EXCLUDED"})
		}
	}
	return captured, nil
}

// retainScopeIdentities keeps expected identity history without asserting fresh availability.
// An unavailable observation with no verified volume link retains only its previous expected link.
func retainScopeIdentities(previous, current profileSources) profileSources {
	current.Volumes = append([]contracts.Volume{}, current.Volumes...)
	current.Shares = append([]contracts.Share{}, current.Shares...)
	volumes := map[string]bool{}
	for _, v := range current.Volumes {
		volumes[v.ID] = true
	}
	for _, v := range previous.Volumes {
		if !volumes[v.ID] {
			v.State = "unmounted"
			current.Volumes = append(current.Volumes, v)
		}
	}
	shares := map[string]int{}
	for i, s := range current.Shares {
		shares[s.ID] = i
	}
	for _, s := range previous.Shares {
		if i, ok := shares[s.ID]; ok {
			if current.Shares[i].VolumeID == "" && current.Shares[i].State != "reachable" && current.Shares[i].PhysicalPath == s.PhysicalPath {
				current.Shares[i].VolumeID = s.VolumeID
			}
			continue
		}
		s.State = "missing"
		s.Readable = false
		s.FileStationPath = nil
		s.Encrypted = nil
		s.Locked = nil
		current.Shares = append(current.Shares, s)
	}
	return current
}

// loadSources rejects pre-contract profiles until an explicit privileged reimport.
func loadSources(raw string) (profileSources, error) {
	var sources profileSources
	if json.Unmarshal([]byte(raw), &sources) != nil || sources.Version != 1 || sources.Mode != "native_inventory" && sources.Mode != "isolated_roots" {
		return profileSources{}, errors.New("PROFILE_REIMPORT_REQUIRED")
	}
	return sources, nil
}

// prepareImport accepts native catalog IDs or explicitly classified privileged fixture roots.
func prepareImport(ctx context.Context, in Import, state string, read catalogReader) (profileSources, inventory.NormalizedScope, error) {
	var sources profileSources
	switch in.SourceMode {
	case "native_inventory":
		// Native source authority never accepts caller-supplied path or type overrides.
		if len(in.Roots) != 0 || len(in.VolumeIDs) != 0 || len(in.RootKinds) != 0 {
			return sources, inventory.NormalizedScope{}, ErrInvalid
		}
		var err error
		sources, err = read(ctx, profileSources{})
		if err != nil {
			return sources, inventory.NormalizedScope{}, err
		}
	case "isolated_roots":
		if in.Profile.Scope.Kind == "nas" || len(in.Roots) == 0 || len(in.Roots) > 16 || len(in.Roots) != len(in.VolumeIDs) || len(in.Roots) != len(in.RootKinds) {
			return sources, inventory.NormalizedScope{}, ErrInvalid
		}
		sources = profileSources{Version: 1, Mode: in.SourceMode, Qualified: []inventory.NormalizedRoot{}}
		selected := map[string]bool{}
		for _, id := range in.Profile.Scope.TargetIDs {
			selected[id] = true
		}
		if len(selected) != len(in.Roots) {
			return sources, inventory.NormalizedScope{}, ErrInvalid
		}
		for id, root := range in.Roots {
			if !selected[id] || !identifier.MatchString(id) || !identifier.MatchString(in.VolumeIDs[id]) || !safeSourcePath(root, state, true) {
				return sources, inventory.NormalizedScope{}, ErrInvalid
			}
			r := inventory.NormalizedRoot{TargetID: id, Kind: in.RootKinds[id], VolumeID: in.VolumeIDs[id], PhysicalPath: root, State: "reachable"}
			if r.Kind == "share" {
				shareID := id
				r.ShareID = &shareID
			}
			sources.Qualified = append(sources.Qualified, r)
		}
	default:
		return sources, inventory.NormalizedScope{}, ErrInvalid
	}
	normalized, err := normalizeSources(in.Profile.Scope, sources)
	if err != nil {
		return sources, normalized, err
	}
	for _, r := range normalized.Roots {
		if !safeSourcePath(r.PhysicalPath, state, in.SourceMode == "isolated_roots") {
			return sources, normalized, ErrInvalid
		}
	}
	return sources, normalized, nil
}

// safeSourcePath excludes system and package state independently of source authority.
func safeSourcePath(root, state string, isolated bool) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" || isolated && strings.Count(root, string(os.PathSeparator)) < 2 {
		return false
	}
	if root == state || strings.HasPrefix(root, state+string(os.PathSeparator)) {
		return false
	}
	for _, blocked := range []string{"/proc", "/sys", "/dev", "/etc", "/usr", "/bin", "/sbin", "/var/packages"} {
		if overlap(root, blocked) {
			return false
		}
	}
	return true
}
