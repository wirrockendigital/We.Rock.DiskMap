package security

import (
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"path"
	"strconv"
	"strings"
)

var ErrScope = errors.New("SCOPE_MISMATCH")

// RelativeBytes validates original Unix pathname bytes without decoding or following symlinks.
func RelativeBytes(value string) error {
	if value == "" || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") || value == ".." || strings.HasPrefix(value, "../") || path.Clean(value) != value {
		return ErrScope
	}
	return nil
}

// ValidateNode binds database membership, original path, source target and volume to one generation.
func ValidateNode(node contracts.Node, snapshot, nodeID, target, volume, rawPath string) error {
	if !ValidID(snapshot) || !ValidNodeID(nodeID) || !ValidID(target) || !ValidID(volume) || RelativeBytes(rawPath) != nil || node.SnapshotID != snapshot || node.ID != nodeID || node.VolumeID != volume {
		return ErrScope
	}
	if contracts.ValidateNodeType(node) != nil {
		return ErrScope
	}
	if node.ShareID != nil && *node.ShareID != target {
		return ErrScope
	}
	return nil
}

// ValidNodeID accepts only canonical positive signed 64-bit generation-local locators.
func ValidNodeID(value string) bool {
	v, err := strconv.ParseInt(value, 10, 64)
	return err == nil && v > 0 && strconv.FormatInt(v, 10) == value
}
