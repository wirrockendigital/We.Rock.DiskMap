package inventory

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"path/filepath"
	"strings"
)

// EncryptedMount records a qualified kernel relationship, never encryption options or keys.
// Backing and volume descriptors remain stable across a same-boot ecryptfs remount.
type EncryptedMount struct {
	Version  int                 `json:"version"`
	VolumeID string              `json:"volumeId"`
	Volume   scanner.RootBinding `json:"volume"`
	Backing  scanner.RootBinding `json:"backing"`
	Exposed  scanner.RootBinding `json:"exposed"`
	Mount    Mount               `json:"mount"`
}

// ValidEncryptedMount requires the exact qualified DSM relationship, not a path prefix.
func ValidEncryptedMount(p EncryptedMount, v contracts.Volume, s contracts.Share) bool {
	if p.Version != 1 || !filepath.IsAbs(v.MountPath) || filepath.Clean(v.MountPath) != v.MountPath || v.MountPath == "/" || v.ID != s.VolumeID || p.VolumeID != v.ID || s.Encrypted == nil || !*s.Encrypted || s.Locked == nil || *s.Locked || !s.Readable || s.State != "reachable" || s.FileStationPath == nil || !strings.HasPrefix(s.ID, "share-") {
		return false
	}
	if !uuidPattern.MatchString(s.ID[6:]) || s.IdentityEvidence != "DSM90080:SYNO@.attr:"+s.ID[6:] || filepath.Dir(s.PhysicalPath) != v.MountPath || *s.FileStationPath != "/"+filepath.Base(s.PhysicalPath) {
		return false
	}
	for _, b := range []scanner.RootBinding{p.Volume, p.Backing, p.Exposed} {
		if b.Device == "" || b.Inode == "" || b.MountID == "" {
			return false
		}
	}
	return p.Volume.Path == v.MountPath && p.Backing.Path == filepath.Join(v.MountPath, "@"+filepath.Base(s.PhysicalPath)+"@") && p.Exposed.Path == s.PhysicalPath && p.Backing.MountID == p.Volume.MountID && p.Mount.Path == s.PhysicalPath && p.Mount.Filesystem == "ecryptfs" && p.Mount.Root == "/" && p.Mount.Source == p.Backing.Path && p.Mount.ParentID == p.Volume.MountID && p.Mount.ID == p.Exposed.MountID
}
