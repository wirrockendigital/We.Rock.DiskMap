package inventory

import "github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"

// VisibleVolumes projects navigation containers from currently authorized shares only.
func VisibleVolumes(volumes []contracts.Volume, shares []contracts.Share) []contracts.Volume {
	allowed := make(map[string]bool, len(shares))
	for _, share := range shares {
		if share.Readable && share.State == "reachable" {
			allowed[share.VolumeID] = true
		}
	}
	result := make([]contracts.Volume, 0, len(volumes))
	for _, volume := range volumes {
		if allowed[volume.ID] {
			result = append(result, volume)
		}
	}
	return result
}
