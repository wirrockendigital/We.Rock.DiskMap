package jobs

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// copyMountProofs prevents capture failures from mutating the persisted authority in memory.
func copyMountProofs(source map[string]inventory.EncryptedMount) map[string]inventory.EncryptedMount {
	out := map[string]inventory.EncryptedMount{}
	for id, p := range source {
		out[id] = p
	}
	return out
}

// qualifiedMount binds fresh kernel evidence to exactly one authoritative native share and volume.
func qualifiedMount(s profileSources, id string) (inventory.EncryptedMount, bool) {
	p, ok := s.EncryptedMounts[id]
	if !ok || s.Mode != "native_inventory" || !validBoot(s.BootID) {
		return p, false
	}
	count := 0
	for _, share := range s.Shares {
		if share.ID == id {
			for _, volume := range s.Volumes {
				if volume.ID == share.VolumeID && inventory.ValidEncryptedMount(p, volume, share) {
					count++
				}
			}
		}
	}
	return p, count == 1
}

// mayRemount accepts only a previously captured same-boot backing object and unchanged stable mapping.
func mayRemount(before, after profileSources, r inventory.NormalizedRoot, b scanner.RootBinding) bool {
	old, ok := before.BindingMounts[r.TargetID]
	fresh, valid := qualifiedMount(after, r.TargetID)
	return ok && valid && r.Kind == "share" && old.Version == 1 && validBoot(before.BindingBoots[r.TargetID]) && before.BindingBoots[r.TargetID] == after.BootID && before.BindingIdentities[r.TargetID] != "" && before.BindingIdentities[r.TargetID] == nativeRootIdentity(after, r) && old.VolumeID == fresh.VolumeID && old.Volume == fresh.Volume && old.Backing == fresh.Backing && old.Exposed.Path == b.Path && fresh.Exposed == b
}

// stampMountProof never synthesizes authority when the native provider cannot prove it.
func stampMountProof(s *profileSources, r inventory.NormalizedRoot, b scanner.RootBinding) {
	p, ok := qualifiedMount(*s, r.TargetID)
	if !ok || p.Exposed != b {
		return
	}
	if s.BindingMounts == nil {
		s.BindingMounts = map[string]inventory.EncryptedMount{}
	}
	s.BindingMounts[r.TargetID] = p
}

// sameCapturedMount keeps the complete observed relationship fixed within an accepted run.
func sameCapturedMount(before, after profileSources, id string) bool {
	old, exists := before.EncryptedMounts[id]
	if !exists {
		return true
	}
	fresh, ok := qualifiedMount(after, id)
	return ok && old == fresh
}

// mountAuthorityUnchanged also rejects backing replacement when the kernel reuses an exposed ID.
func mountAuthorityUnchanged(before, after profileSources, r inventory.NormalizedRoot, b scanner.RootBinding) bool {
	old, exists := before.BindingMounts[r.TargetID]
	if !exists || before.BindingBoots[r.TargetID] != after.BootID {
		return true
	}
	fresh, ok := qualifiedMount(after, r.TargetID)
	return ok && old.VolumeID == fresh.VolumeID && old.Volume == fresh.Volume && old.Backing == fresh.Backing && fresh.Exposed == b
}
