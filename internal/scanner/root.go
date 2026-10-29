package scanner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// RootBinding pins a private bootstrap mapping to its observed filesystem object.
// Device numbers identify this mounted environment; they are not physical volume UUIDs.
type RootBinding struct {
	Path    string `json:"path"`
	Device  string `json:"device"`
	Inode   string `json:"inode"`
	MountID string `json:"mountId"`
}

// OpenBoundRoot opens every component relative to a held descriptor without following links.
func OpenBoundRoot(path string) (*os.Root, RootBinding, error) {
	var binding RootBinding
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, binding, errors.New("invalid root")
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open("/", ancestorOpenFlags(), 0)
	if err != nil {
		return nil, binding, err
	}
	// Each ancestor stays open until its verified child descriptor has been acquired.
	components := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, component := range components {
		componentFlags := ancestorOpenFlags()
		if i == len(components)-1 {
			componentFlags = flags
		}
		child, e := unix.Openat(fd, component, componentFlags, 0)
		unix.Close(fd)
		if e != nil {
			return nil, binding, e
		}
		fd = child
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err = unix.Fstat(fd, &stat); err != nil {
		return nil, binding, err
	}
	binding = RootBinding{Path: path, Device: strconv.FormatUint(uint64(stat.Dev), 10), Inode: strconv.FormatUint(uint64(stat.Ino), 10)}
	// Go exposes no public Root-from-fd constructor. These kernel-owned descriptor
	// aliases reopen this held descriptor rather than resolving the mutable source path.
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	root, err := os.OpenRoot(prefix + strconv.Itoa(fd))
	if err != nil {
		return nil, binding, err
	}
	info, err := root.Stat(".")
	if err != nil {
		root.Close()
		return nil, binding, err
	}
	observed := info.Sys().(*syscall.Stat_t)
	if uint64(observed.Dev) != uint64(stat.Dev) || uint64(observed.Ino) != uint64(stat.Ino) {
		root.Close()
		return nil, binding, fmt.Errorf("descriptor identity mismatch")
	}
	file, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, binding, err
	}
	binding.MountID, err = MountIdentity(file)
	file.Close()
	if err != nil {
		root.Close()
		return nil, binding, err
	}
	return root, binding, nil
}

// openVerifiedRoot rejects a replaced root before any metadata enumeration occurs.
func openVerifiedRoot(expected RootBinding) (*os.Root, error) {
	if expected.MountID == "" {
		return nil, errors.New("ROOT_REBIND_REQUIRED")
	}
	root, observed, err := OpenBoundRoot(expected.Path)
	if err != nil {
		return nil, err
	}
	if observed != expected {
		root.Close()
		return nil, errSourceChanged
	}
	return root, nil
}
