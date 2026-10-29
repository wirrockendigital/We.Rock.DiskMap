package jobs

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// qualifySubvolumes captures current native registry shares covered by a volume selection.
// Failed qualifications grant no traversal permission; the scanner records any encountered gap.
func qualifySubvolumes(ctx context.Context, manifest *scopemanifest.ScopeManifest, sources profileSources) {
	manifest.QualifiedSubvolumes = []scopemanifest.QualifiedSubvolume{}
	if sources.Mode != "native_inventory" {
		return
	}
	covered := map[string]bool{}
	for _, id := range manifest.CoveredTargetIDs {
		covered[id] = true
	}
	for _, root := range manifest.Roots {
		if root.Kind != "volume" || root.Binding == nil {
			continue
		}
		for _, share := range sources.Shares {
			if ctx.Err() != nil {
				return
			}
			if !covered[share.ID] || share.VolumeID != root.VolumeID || !share.Readable || share.State != "reachable" || share.Locked != nil && *share.Locked || !strings.HasPrefix(share.PhysicalPath, root.PhysicalPath+"/") {
				continue
			}
			relative, err := filepath.Rel(root.PhysicalPath, share.PhysicalPath)
			if err != nil {
				continue
			}
			descriptor, binding, err := scanner.OpenBoundRoot(share.PhysicalPath)
			if err != nil {
				continue
			}
			file, err := descriptor.Open(".")
			if err != nil {
				descriptor.Close()
				continue
			}
			tree, btrfs, err := scanner.NativeSubvolumeID(file)
			file.Close()
			descriptor.Close()
			if err != nil || !btrfs || binding.MountID != root.Binding.MountID {
				continue
			}
			manifest.QualifiedSubvolumes = append(manifest.QualifiedSubvolumes, scopemanifest.QualifiedSubvolume{TargetID: root.TargetID, SourceID: share.ID, RelativePath: filepath.ToSlash(relative), TreeID: tree, Binding: scopemanifest.RootIdentity{Path: binding.Path, Device: binding.Device, Inode: binding.Inode, MountID: binding.MountID}})
		}
	}
}
