package inventory

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

// ObserveRecycle checks one share-level directory as the package account, never its descendants.
// Unknown is deliberately distinct from an absent bin or a confirmed permission denial.
func ObserveRecycle(ctx context.Context, path string) string {
	if ctx.Err() != nil {
		return "unknown"
	}
	root, _, err := scanner.OpenBoundRoot(path)
	if err != nil {
		return "unknown"
	}
	defer root.Close()
	parent, err := root.Open(".")
	if err != nil {
		return "unknown"
	}
	defer parent.Close()
	return observeRecycleAt(ctx, parent)
}

// A held parent and no-follow open prevent a replacement link from escaping the observed share.
func observeRecycleAt(ctx context.Context, parent *os.File) string {
	if ctx.Err() != nil {
		return "unknown"
	}
	var info unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), "#recycle", &info, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return "missing"
	}
	if err != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "unknown"
	}
	fd, err := unix.Openat(int(parent.Fd()), "#recycle", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return "denied"
	}
	if err != nil {
		return "unknown"
	}
	file := os.NewFile(uintptr(fd), "#recycle")
	defer file.Close()
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || opened.Dev != info.Dev || opened.Ino != info.Ino {
		return "unknown"
	}
	// Listing and traversal are both required; a list-only ACL must not look like an empty readable bin.
	if err = unix.Faccessat(fd, ".", unix.R_OK|unix.X_OK, unix.AT_EACCESS); errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return "denied"
	} else if err != nil {
		return "unknown"
	}
	// One directory entry is sufficient to distinguish an empty readable bin from denied listing.
	_, err = file.Readdirnames(1)
	if errors.Is(err, os.ErrPermission) {
		return "denied"
	}
	if err != nil && !errors.Is(err, io.EOF) || ctx.Err() != nil {
		return "unknown"
	}
	return "readable"
}
