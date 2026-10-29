//go:build linux

package scanner

import (
	"golang.org/x/sys/unix"
	"os"
)

// inspectEntry checks the exact no-follow handle rather than trusting a directory-entry type.
func inspectEntry(parent *os.File, name, mountID string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, metadataOpenFlags(), 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	observed, err := MountIdentity(file)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if mountID == "" || observed != mountID {
		file.Close()
		// A successful no-follow stat may qualify an exclusion, never traversal.
		return nil, info, errMountChanged
	}
	return file, info, nil
}
