package scanner

import (
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"golang.org/x/sys/unix"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// descriptorRoot creates a traversal anchor from an already held descriptor.
func descriptorRoot(file *os.File) (*os.Root, error) {
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	return os.OpenRoot(prefix + strconv.FormatUint(uint64(file.Fd()), 10))
}

// openDirectory converts only an already checked child into a readable no-follow directory handle.
func openDirectory(parent *os.File, name string, expected os.FileInfo, mountID string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(expected, info) || ObjectIdentity(expected) != ObjectIdentity(info) {
		file.Close()
		return nil, errSourceChanged
	}
	observed, err := MountIdentity(file)
	if err != nil {
		file.Close()
		return nil, err
	}
	if observed != mountID {
		file.Close()
		return nil, errMountChanged
	}
	return file, nil
}

// InspectBoundPath validates every original component under a fresh complete root binding.
func InspectBoundPath(binding RootBinding, relative string) (os.FileInfo, error) {
	if security.RelativeBytes(relative) != nil {
		return nil, security.ErrScope
	}
	root, err := openVerifiedRoot(binding)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	current, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { current.Close() }()
	if relative == "." {
		return current.Stat()
	}
	parts := strings.Split(relative, "/")
	for i, name := range parts {
		handle, info, e := inspectEntry(current, name, binding.MountID)
		if e != nil {
			return nil, e
		}
		if handle != nil {
			handle.Close()
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("SYMLINK_FORBIDDEN")
		}
		if i == len(parts)-1 {
			return info, nil
		}
		next, e := openDirectory(current, name, info, binding.MountID)
		if e != nil {
			return nil, e
		}
		current.Close()
		current = next
	}
	return nil, security.ErrScope
}
