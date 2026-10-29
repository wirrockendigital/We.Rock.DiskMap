//go:build !linux

package inventory

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// DiscoverShares refuses native DSM inventory on an unrelated host platform.
func DiscoverShares(context.Context, []contracts.Volume, []contracts.Share) (ShareResult, error) {
	return ShareResult{}, errShareRegistry
}
