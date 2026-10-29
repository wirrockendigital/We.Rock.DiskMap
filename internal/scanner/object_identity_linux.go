package scanner

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ObjectIdentity records metadata identity without opening regular file contents.
func ObjectIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec)
}

// SameObject excludes directory ctime: removing a child legitimately changes it.
func SameObject(info os.FileInfo, identity string) bool {
	current := ObjectIdentity(info)
	if current == "" || identity == "" {
		return false
	}
	if info.IsDir() {
		a, b := strings.Split(current, ":"), strings.Split(identity, ":")
		return len(a) == 4 && len(b) == 4 && a[0] == b[0] && a[1] == b[1]
	}
	return current == identity
}
