//go:build linux

package jobs

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// init provides synthetic identity only in Linux test binaries on non-Btrfs CI filesystems.
// Real Btrfs always exercises the native IOCTL adapter; this is not deployment evidence.
func init() {
	filterCacheObject = func(file *os.File) string {
		var fs unix.Statfs_t
		var st unix.Stat_t
		if unix.Fstatfs(int(file.Fd()), &fs) != nil || unix.Fstat(int(file.Fd()), &st) != nil {
			return ""
		}
		if fs.Type == unix.BTRFS_SUPER_MAGIC {
			return nativeFilterCacheObject(file)
		}
		return persistentFilterObject(fmt.Sprintf("btrfs:%032x:256", uint64(st.Dev)+1), st)
	}
}
