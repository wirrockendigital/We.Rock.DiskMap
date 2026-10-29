package index

import (
	"path/filepath"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ValidateQualifiedSubvolumes checks private captured authority before scanning and sealing.
func ValidateQualifiedSubvolumes(manifest scopemanifest.ScopeManifest) error {
	if len(manifest.QualifiedSubvolumes) > 2048 {
		return ErrGeneration
	}
	covered := map[string]bool{}
	roots := map[string]scopemanifest.ManifestRoot{}
	for _, id := range manifest.CoveredTargetIDs {
		covered[id] = true
	}
	for _, root := range manifest.Roots {
		roots[root.TargetID] = root
	}
	seen := map[string]bool{}
	for _, q := range manifest.QualifiedSubvolumes {
		root, ok := roots[q.TargetID]
		id, err := strconv.ParseUint(q.TreeID, 10, 64)
		key := q.TargetID + "\x00" + q.RelativePath
		if !ok || root.Kind != "volume" || root.Binding == nil || !covered[q.SourceID] || !security.ValidID(q.SourceID) || q.RelativePath == "." || security.RelativeBytes(q.RelativePath) != nil || seen[key] || err != nil || id == 0 || strconv.FormatUint(id, 10) != q.TreeID || q.Binding.Path != filepath.Join(root.PhysicalPath, filepath.FromSlash(q.RelativePath)) || q.Binding.MountID != root.Binding.MountID || q.Binding.MountID == "" || q.Binding.Device == "" || q.Binding.Inode == "" {
			return ErrGeneration
		}
		seen[key] = true
	}
	return nil
}
