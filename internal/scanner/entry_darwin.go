//go:build darwin

package scanner

import (
	"fmt"
	"os"
	"syscall"
)

// inspectEntry uses no-follow metadata on macOS, where O_PATH is unavailable.
func inspectEntry(parent *os.File, name, mountID string) (*os.File, os.FileInfo, error) {
	root, err := descriptorRoot(parent)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if fmt.Sprintf("darwin-fsid:%d", info.Sys().(*syscall.Stat_t).Dev) != mountID {
		return nil, nil, errMountChanged
	}
	return nil, info, nil
}
