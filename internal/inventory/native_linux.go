//go:build linux

package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

// Discover observes only the daemon's mount namespace and operating identity.
func Discover(ctx context.Context, previous []contracts.Volume) (Result, error) {
	before, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return Result{}, errors.New("MOUNTINFO_UNAVAILABLE")
	}
	mounts, err := ParseMountInfo(before)
	if err != nil {
		return Result{}, err
	}
	result, err := Collect(ctx, mounts, previous, probeNative, time.Now())
	if err != nil {
		return result, err
	}
	after, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return Result{}, errors.New("MOUNTINFO_UNAVAILABLE")
	}
	next, err := ParseMountInfo(after)
	if err != nil {
		return Result{}, err
	}
	// A mount change during collection invalidates the assembled inventory as a whole.
	if signature(mounts) != signature(next) {
		return Result{}, errors.New("MOUNT_CHANGED")
	}
	return result, nil
}

// signature ignores unrelated temporary mounts but preserves every candidate's source binding.
func signature(mounts []Mount) string {
	var value strings.Builder
	for _, m := range mounts {
		if volumePath.MatchString(m.Path) {
			fmt.Fprintf(&value, "%q;", m)
		}
	}
	return value.String()
}

// probeNative obtains FSID and capacity from the same descriptor, not a mutable device pathname.
func probeNative(ctx context.Context, m Mount) (Observation, error) {
	var out Observation
	if err := ctx.Err(); err != nil {
		return out, err
	}
	root, _, err := scanner.OpenBoundRoot(m.Path)
	if err != nil {
		return out, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return out, err
	}
	defer file.Close()
	fdinfo, err := os.ReadFile("/proc/self/fdinfo/" + strconv.FormatUint(uint64(file.Fd()), 10))
	if err != nil {
		return out, err
	}
	for _, line := range strings.Split(string(fdinfo), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "mnt_id:" {
			out.MountID = fields[1]
		}
	}
	if out.MountID != m.ID {
		return out, errors.New("MOUNT_CHANGED")
	}
	// Linux UAPI btrfs.h: _IOR(0x94,31,struct btrfs_ioctl_fs_info_args), size 1024.
	// max_id and num_devices precede the 16-byte FSID at offset 16.
	var info [1024]byte
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), uintptr(0x8400941f), uintptr(unsafe.Pointer(&info[0])))
	if errno != 0 {
		return out, errno
	}
	fsid := info[16:32]
	out.UUID = fmt.Sprintf("%x-%x-%x-%x-%x", fsid[0:4], fsid[4:6], fsid[6:8], fsid[8:10], fsid[10:16])
	if stat, err := os.Stat("/sys/fs/btrfs/" + out.UUID); err != nil || !stat.IsDir() {
		return out, errors.New("IDENTITY_UNVERIFIED")
	}
	var fs unix.Statfs_t
	if err = unix.Fstatfs(int(file.Fd()), &fs); err != nil {
		return out, err
	}
	if fs.Type != unix.BTRFS_SUPER_MAGIC || fs.Frsize <= 0 {
		return out, errors.New("UNSUPPORTED_FILESYSTEM")
	}
	out.BlockSize = uint64(fs.Frsize)
	out.Blocks = fs.Blocks
	out.Free = fs.Bfree
	out.Available = fs.Bavail
	return out, nil
}
