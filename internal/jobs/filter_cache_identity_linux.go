//go:build linux

package jobs

import (
	"fmt"
	"os"
	"unsafe"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

// nativeFilterCacheObject qualifies Btrfs UUID plus containing subvolume on the same held file descriptor.
func nativeFilterCacheObject(f *os.File) string {
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(int(f.Fd()), &st) != nil || unix.Fstatfs(int(f.Fd()), &fs) != nil {
		return ""
	}
	if fs.Type == unix.BTRFS_SUPER_MAGIC {
		// Linux UAPI: BTRFS_IOC_FS_INFO has a 1024-byte payload and a 16-byte UUID at offset 16.
		var info [1024]byte
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), uintptr(0x8400941f), uintptr(unsafe.Pointer(&info[0])))
		if errno != 0 {
			return ""
		}
		subvolume, known, err := scanner.NativeSubvolumeID(f)
		if err != nil || !known {
			return ""
		}
		return persistentFilterObject(fmt.Sprintf("btrfs:%x:%s", info[16:32], subvolume), st)
	}
	// Unqualified filesystems never receive a persistent ownership identity.
	return ""
}
