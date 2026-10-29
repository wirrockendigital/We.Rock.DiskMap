package jobs

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

// jobWorkDir pins both directory identities before SQLite creates any working files.
type jobWorkDir struct {
	parent, dir       *os.File
	staging, snapshot string
}

// createJobWork uses no-follow directory descriptors and an exclusive random staging name.
func createJobWork(parentPath, staging, snapshot string) (*jobWorkDir, error) {
	if !identifier.MatchString(snapshot) || staging != snapshot+".staging" {
		return nil, ErrInvalid
	}
	if err := os.MkdirAll(parentPath, 0700); err != nil {
		return nil, err
	}
	fd, err := unix.Open(parentPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	parent := os.NewFile(uintptr(fd), "generations")
	if err = unix.Mkdirat(fd, staging, 0700); err != nil {
		parent.Close()
		return nil, err
	}
	child, err := unix.Openat(fd, staging, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		parent.Close()
		return nil, err
	}
	return &jobWorkDir{parent: parent, dir: os.NewFile(uintptr(child), "job-work"), staging: staging, snapshot: snapshot}, nil
}

// Close releases identities after all worker cleanup and publication decisions are finished.
func (w *jobWorkDir) Close() {
	if w != nil {
		w.dir.Close()
		w.parent.Close()
	}
}

// cleanupCancelledGeneration never resolves file deletion through an exchangeable directory path.
func (w *jobWorkDir) cleanupCancelledGeneration() error {
	if w == nil {
		return nil
	}
	var own unix.Stat_t
	if err := unix.Fstat(int(w.dir.Fd()), &own); err != nil {
		return err
	}
	if own.Uid != uint32(os.Geteuid()) || own.Mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrUnavailable
	}
	name := ""
	for _, candidate := range []string{w.staging, w.snapshot} {
		var current unix.Stat_t
		err := unix.Fstatat(int(w.parent.Fd()), candidate, &current, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if current.Dev != own.Dev || current.Ino != own.Ino || current.Mode&unix.S_IFMT != unix.S_IFDIR {
			return ErrUnavailable
		}
		name = candidate
	}
	if name == "" {
		return ErrUnavailable
	}
	for _, file := range []string{"index.db-journal", "index.db-wal", "index.db-shm", "index.db"} {
		var info unix.Stat_t
		err := unix.Fstatat(int(w.dir.Fd()), file, &info, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Uid != uint32(os.Geteuid()) || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
			return ErrUnavailable
		}
		if err = unix.Unlinkat(int(w.dir.Fd()), file, 0); err != nil {
			return err
		}
	}
	// Recheck the public name before unlinking only an empty directory; a symlink is never followed.
	var current unix.Stat_t
	if err := unix.Fstatat(int(w.parent.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return err
	}
	if current.Dev != own.Dev || current.Ino != own.Ino {
		return ErrUnavailable
	}
	return unix.Unlinkat(int(w.parent.Fd()), filepath.Base(name), unix.AT_REMOVEDIR)
}
