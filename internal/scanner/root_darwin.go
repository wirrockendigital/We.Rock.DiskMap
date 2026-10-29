//go:build darwin

package scanner

import "golang.org/x/sys/unix"

// ancestorOpenFlags uses Darwin's SDK O_SEARCH (O_EXEC | O_DIRECTORY).
// x/sys does not yet export O_EXEC, whose platform ABI value is 0x40000000.
func ancestorOpenFlags() int {
	const openExecute = 0x40000000
	return openExecute | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
}
