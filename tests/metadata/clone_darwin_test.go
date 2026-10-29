//go:build darwin

package metadata

import "golang.org/x/sys/unix"

// cloneFile attempts the native copy-on-write primitive without silently copying bytes.
func cloneFile(source, destination string) error { return unix.Clonefile(source, destination, 0) }
