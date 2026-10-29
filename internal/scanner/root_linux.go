//go:build linux

package scanner

import "golang.org/x/sys/unix"

// ancestorOpenFlags needs search permission, not directory listing permission.
func ancestorOpenFlags() int {
	return unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
}

// metadataOpenFlags obtains metadata handles without opening file contents or following links.
func metadataOpenFlags() int { return unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC }
