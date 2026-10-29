//go:build darwin

package jobs

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// nativeFilterCacheObject keeps local Darwin fixtures boot-bound; only the Btrfs adapter claims reboot stability.
func nativeFilterCacheObject(f *os.File) string {
	var st unix.Stat_t
	var fs unix.Statfs_t
	if unix.Fstat(int(f.Fd()), &st) != nil || unix.Fstatfs(int(f.Fd()), &fs) != nil {
		return ""
	}
	boot, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return persistentFilterObject(fmt.Sprintf("darwin-boot:%s:%08x%08x", strings.ToLower(strings.ReplaceAll(strings.TrimSpace(boot), "-", "")), uint32(fs.Fsid.Val[0]), uint32(fs.Fsid.Val[1])), st)
}
