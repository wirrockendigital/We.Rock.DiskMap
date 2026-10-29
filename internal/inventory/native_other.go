//go:build !linux

package inventory

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Discover explicitly refuses host-platform inventory outside the native Linux runtime.
func Discover(context.Context, []contracts.Volume) (Result, error) {
	return Result{}, errors.New("UNSUPPORTED_PLATFORM")
}
