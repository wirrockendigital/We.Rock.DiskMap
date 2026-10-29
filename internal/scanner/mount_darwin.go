//go:build darwin

package scanner

import (
	"fmt"
	"os"
	"syscall"
)

// MountIdentity is a local macOS filesystem test identity; native Linux uses the kernel mount ID.
func MountIdentity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("darwin-fsid:%d", info.Sys().(*syscall.Stat_t).Dev), nil
}
