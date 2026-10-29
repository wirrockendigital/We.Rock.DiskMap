package query

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"path"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// colorBranchKey qualifies historical raw-byte identity without granting navigation authority.
// Explicit/synthetic roots do not establish stable native source identity across captures.
// colorBranchKeyForTarget uses authority supplied by the qualified numeric tree reader.
func (e *engine) colorBranchKeyForTarget(n contracts.Node, rawPath []byte, target string) *string {
	provenance := e.h.Manifest.Provenance
	if provenance.Version != 1 || provenance.Authority != "native-inventory" {
		return nil
	}
	for _, root := range provenance.Roots {
		if root.VolumeID != n.VolumeID || root.Binding == nil || root.SourceID == nil {
			continue
		}
		if root.TargetID != target {
			continue
		}
		source, relative := *root.SourceID, string(rawPath)
		// The most specific qualified share wins, independently of volume versus share scan scope.
		longest := -1
		for _, area := range root.Areas {
			prefix := string(area.RelativePath)
			if area.Qualification != "qualified" || area.State != "reachable" || area.Binding == nil || area.SourceID == nil || area.VolumeID != root.VolumeID {
				continue
			}
			if len(prefix) > longest && (relative == prefix || strings.HasPrefix(relative, prefix+"/")) {
				source = *area.SourceID
				longest = len(prefix)
			}
		}
		if longest >= 0 {
			if len(relative) == longest {
				relative = "."
			} else {
				relative = relative[longest+1:]
			}
		}
		// Files and special entries use their containing directory; directories retain their own branch.
		if n.Kind != "directory" {
			relative = path.Dir(relative)
		}
		return opaqueColorKey("diskmap-color-branch-v1", root.VolumeID, source, relative)
	}
	return nil
}

// volumeColorKey colors only a historically bound native volume container, without guessing a path.
func (e *engine) volumeColorKey(volumeID string) *string {
	p := e.h.Manifest.Provenance
	if p.Version != 1 || p.Authority != "native-inventory" {
		return nil
	}
	for _, root := range p.Roots {
		if root.VolumeID == volumeID && root.Binding != nil && root.SourceID != nil {
			return opaqueColorKey("diskmap-color-container-v1", "volume", volumeID)
		}
	}
	return nil
}

// opaqueColorKey length-frames arbitrary raw bytes and separates container and physical branch domains.
func opaqueColorKey(parts ...string) *string {
	h := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		h.Write(length[:])
		h.Write([]byte(part))
	}
	key := "cb1-" + hex.EncodeToString(h.Sum(nil))
	return &key
}
