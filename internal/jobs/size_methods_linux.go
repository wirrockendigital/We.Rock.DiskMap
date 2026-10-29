//go:build linux

package jobs

import (
	"golang.org/x/sys/unix"
	"io"
	"os"
)

// capturedSizeMethods reads bounded trusted platform metadata; missing qualification disables allocation selection.
func capturedSizeMethods() []string {
	read := func(path string) string {
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, 16385))
		if err != nil || len(raw) > 16384 {
			return ""
		}
		return string(raw)
	}
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return []string{"descriptor-no-follow-stat"}
	}
	return qualifiedSizeMethods(read("/etc/VERSION"), read("/etc.defaults/synoinfo.conf"), unix.ByteSliceToString(u.Machine[:]))
}
