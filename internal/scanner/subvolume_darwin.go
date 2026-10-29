//go:build darwin

package scanner

import "os"

// NativeSubvolumeID does not claim Btrfs support on local macOS test filesystems.
func NativeSubvolumeID(_ *os.File) (string, bool, error) { return "", false, nil }
