// Package uninstall removes only the exclusively locked, package-owned data tree.
package uninstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

var ErrUnsafe = errors.New("UNINSTALL_STATE_UNSAFE")
var canonical = regexp.MustCompile(`^/volume[1-9][0-9]*/@appdata/we-rock-diskmap$`)
var allowed = map[string]bool{"control.db": true, "control.db-wal": true, "control.db-shm": true, "control.db-journal": true, "daemon.lock": true, "diskmapd.pid": true, "service.log": true, "inventory.json": true, "upgrade-current.json": true, "upgrades": true, "run": true, "staging": true, "generations": true, "logs": true, "remove.pending": true, "filter-cache": true, "query-cache": true}

// knownPath constrains every nested name to a format produced by this package.
func knownPath(p string, directory bool) bool {
	parts := strings.Split(p, "/")
	if len(parts) == 1 {
		return allowed[p] && directory == (p == "run" || p == "staging" || p == "generations" || p == "logs" || p == "upgrades" || p == "filter-cache" || p == "query-cache")
	}
	hexID := regexp.MustCompile(`^[0-9a-f]{32}$`)
	switch parts[0] {
	case "generations", "staging":
		id := strings.TrimSuffix(parts[1], ".staging")
		if !hexID.MatchString(id) {
			return false
		}
		if len(parts) == 2 {
			return directory
		}
		return len(parts) == 3 && !directory && (parts[2] == "index.db" || parts[2] == "index.db-wal" || parts[2] == "index.db-shm" || parts[2] == "index.db-journal")
	case "query-cache":
		return len(parts) == 2 && !directory && regexp.MustCompile(`^[0-9a-f]{64}\.(bin|tmp)$`).MatchString(parts[1])
	case "filter-cache":
		return len(parts) == 2 && !directory && regexp.MustCompile(`^[0-9a-f]{64}\.(db|owner)(-journal|-wal|-shm)?$`).MatchString(parts[1])
	case "logs":
		return len(parts) == 2 && !directory && regexp.MustCompile(`^slot-[0-3]\.jsonl$`).MatchString(parts[1])
	case "upgrades":
		if !hexID.MatchString(parts[1]) {
			return false
		}
		if len(parts) == 2 {
			return directory
		}
		if len(parts) != 3 || directory {
			return false
		}
		n := parts[2]
		return n == "manifest.json" || n == "control.before.db" || n == "inventory.before.json" || n == "candidate.db" || n == "candidate.db-journal" || n == "manifest.json.tmp" || regexp.MustCompile(`^manifest\.json\.tmp-[0-9a-f]{32}$`).MatchString(n) || regexp.MustCompile(`^recovery-displaced-[0-9a-f]{32}\.db$`).MatchString(n)
	}
	return false
}

type entry struct {
	path      string
	info      os.FileInfo
	parent    os.FileInfo
	directory bool
}

// CheckReady prevents partial removal from being mistaken for a new or healthy installation.
func CheckReady(state string) error {
	_, err := os.Lstat(filepath.Join(state, "remove.pending"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return ErrUnsafe
}

// Remove accepts only the canonical DSM-owned application data location, never source roots.
func Remove(ctx context.Context, state string) error {
	resolved, err := filepath.EvalSymlinks(state)
	if err != nil || !canonical.MatchString(resolved) || os.Geteuid() == 0 {
		return ErrUnsafe
	}
	return removeOwned(ctx, resolved, nil)
}

// openDir walks no-follow components relative to a held root and never follows a substituted link.
func openDir(root *os.File, path string) (*os.File, error) {
	fd, err := unix.Dup(int(root.Fd()))
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			unix.Close(fd)
			return nil, ErrUnsafe
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "owned-directory"), nil
}

// owner rejects shared files, permissive objects and every special file including symlinks.
func owner(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Geteuid()) && info.Mode().Perm()&0077 == 0 && (info.IsDir() || (info.Mode().IsRegular() && st.Nlink == 1))
}

// removeOwned separates complete validation from deletion; the optional hook is test-only.
func removeOwned(ctx context.Context, state string, beforeDelete func()) error {
	return removeOwnedWithMarkerIO(ctx, state, beforeDelete, unix.Fsync, unix.Close)
}

// Marker I/O is injectable only at this private boundary for deterministic persistence-failure tests.
func removeOwnedWithMarkerIO(ctx context.Context, state string, beforeDelete func(), syncMarker, closeMarker func(int) error) error {
	fd, e := unix.Open(state, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return ErrUnsafe
	}
	root := os.NewFile(uintptr(fd), "package-state")
	defer root.Close()
	rootInfo, e := root.Stat()
	if e != nil || !owner(rootInfo) {
		return ErrUnsafe
	}
	mount, e := scanner.MountIdentity(root)
	if e != nil {
		return ErrUnsafe
	}
	lockfd, e := unix.Openat(fd, "daemon.lock", unix.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return ErrUnsafe
	}
	lock := os.NewFile(uintptr(lockfd), "daemon-lock")
	defer lock.Close()
	li, e := lock.Stat()
	if e != nil || !owner(li) || !li.Mode().IsRegular() {
		return ErrUnsafe
	}
	if unix.Flock(lockfd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrUnsafe
	}
	entries := []entry{}
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, base string, depth int) error {
		if depth > 64 {
			return ErrUnsafe
		}
		pi, e := dir.Stat()
		if e != nil || !owner(pi) {
			return ErrUnsafe
		}
		mi, e := scanner.MountIdentity(dir)
		if e != nil || mi != mount {
			return ErrUnsafe
		}
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			names, e := dir.Readdirnames(64)
			if e != nil && e != io.EOF {
				return e
			}
			for _, name := range names {
				if base == "" && !allowed[name] {
					return ErrUnsafe
				}
				p := filepath.Join(base, name)
				var st unix.Stat_t
				if unix.Fstatat(int(dir.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
					return ErrUnsafe
				}
				// Reject special inodes before opening them, even in nonblocking mode.
				if st.Mode&unix.S_IFMT != unix.S_IFREG && st.Mode&unix.S_IFMT != unix.S_IFDIR {
					return ErrUnsafe
				}
				flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
				if st.Mode&unix.S_IFMT == unix.S_IFDIR {
					flags |= unix.O_DIRECTORY
				}
				childfd, e := unix.Openat(int(dir.Fd()), name, flags, 0)
				if e != nil {
					return ErrUnsafe
				}
				child := os.NewFile(uintptr(childfd), "owned-entry")
				info, e := child.Stat()
				childMount, mountErr := scanner.MountIdentity(child)
				if e != nil || !owner(info) || !knownPath(p, info.IsDir()) || mountErr != nil || childMount != mount {
					child.Close()
					return ErrUnsafe
				}
				held := info.Sys().(*syscall.Stat_t)
				if uint64(st.Dev) != uint64(held.Dev) || uint64(st.Ino) != uint64(held.Ino) {
					child.Close()
					return ErrUnsafe
				}
				if info.IsDir() {
					e = walk(child, p, depth+1)
				}
				child.Close()
				if e != nil {
					return e
				}
				if len(entries) >= 100000 {
					return ErrUnsafe
				}
				entries = append(entries, entry{p, info, pi, info.IsDir()})
			}
			if e == io.EOF {
				break
			}
		}
		return nil
	}
	if e = walk(root, "", 0); e != nil {
		return e
	}
	currentRoot, rootErr := os.Lstat(state)
	if rootErr != nil || !os.SameFile(rootInfo, currentRoot) {
		return ErrUnsafe
	}
	// The marker is created only after validating the complete proposed deletion set.
	markerfd, e := unix.Openat(fd, "remove.pending", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW, 0600)
	if e != nil && !errors.Is(e, unix.EEXIST) {
		return ErrUnsafe
	}
	if errors.Is(e, unix.EEXIST) {
		// Retry must persist an earlier marker too, including a prior failed sync attempt.
		markerfd, e = unix.Openat(fd, "remove.pending", unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if e != nil {
			return ErrUnsafe
		}
	}
	var markerStat unix.Stat_t
	statErr := unix.Fstat(markerfd, &markerStat)
	if statErr != nil || markerStat.Uid != uint32(os.Geteuid()) || markerStat.Mode&unix.S_IFMT != unix.S_IFREG || markerStat.Nlink != 1 || markerStat.Mode&0077 != 0 {
		_ = closeMarker(markerfd)
		return ErrUnsafe
	}
	// Always close after sync, but neither failure may permit the first destructive operation.
	syncErr := syncMarker(markerfd)
	closeErr := closeMarker(markerfd)
	if syncErr != nil || closeErr != nil {
		return ErrUnsafe
	}
	if root.Sync() != nil {
		return ErrUnsafe
	}
	if beforeDelete != nil {
		beforeDelete()
	}
	for _, item := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item.path == "daemon.lock" || item.path == "remove.pending" {
			continue
		}
		parent, e := openDir(root, filepath.Dir(item.path))
		if e != nil {
			return ErrUnsafe
		}
		pi, e := parent.Stat()
		parentMount, mountErr := scanner.MountIdentity(parent)
		if e != nil || !os.SameFile(pi, item.parent) || mountErr != nil || parentMount != mount {
			parent.Close()
			return ErrUnsafe
		}
		var st unix.Stat_t
		e = unix.Fstatat(int(parent.Fd()), filepath.Base(item.path), &st, unix.AT_SYMLINK_NOFOLLOW)
		prior := item.info.Sys().(*syscall.Stat_t)
		if e != nil || uint64(st.Dev) != uint64(prior.Dev) || uint64(st.Ino) != uint64(prior.Ino) || st.Uid != prior.Uid || uint32(st.Mode)&unix.S_IFMT != uint32(prior.Mode)&unix.S_IFMT || (!item.directory && st.Nlink != 1) {
			parent.Close()
			return ErrUnsafe
		}
		flags := 0
		if item.directory {
			flags = unix.AT_REMOVEDIR
		}
		e = unix.Unlinkat(int(parent.Fd()), filepath.Base(item.path), flags)
		if e == nil {
			e = parent.Sync()
		}
		parent.Close()
		if e != nil {
			return e
		}
	}
	if e = lock.Truncate(0); e != nil {
		return e
	}
	if e = lock.Sync(); e != nil {
		return e
	}
	// The persistent singleton inode remains; it contains no application configuration or index data.
	if e = unix.Unlinkat(fd, "remove.pending", 0); e != nil {
		return e
	}
	return root.Sync()
}
