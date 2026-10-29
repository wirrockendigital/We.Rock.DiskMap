//go:build !linux

package inventory

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// ObserveEncryptedMounts never invents a DSM mount capability on other platforms.
func ObserveEncryptedMounts(context.Context, []contracts.Volume, []contracts.Share) map[string]EncryptedMount {
	return map[string]EncryptedMount{}
}
