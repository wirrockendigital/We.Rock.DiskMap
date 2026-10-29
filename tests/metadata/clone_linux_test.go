//go:build linux

package metadata

import (
	"golang.org/x/sys/unix"
	"os"
)

// cloneFile records unsupported reflinks rather than substituting an ordinary copy.
func cloneFile(source, destination string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = unix.IoctlFileClone(int(dst.Fd()), int(src.Fd()))
	closeErr := dst.Close()
	if err != nil {
		os.Remove(destination)
		return err
	}
	return closeErr
}
