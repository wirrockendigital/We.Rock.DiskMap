package security

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// RuntimeIdentity rejects accidental privileged service execution; root may only use the CLI.
func RuntimeIdentity(uid, gid int) error {
	if uid <= 0 || gid <= 0 {
		return errors.New("PRIVILEGED_SERVICE_FORBIDDEN")
	}
	return nil
}

// privateObject validates the opened inode before reducing its access permissions.
func privateObject(file *os.File, directory bool) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || info.IsDir() != directory || (!directory && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
		return errors.New("UNSAFE_RUNTIME_OBJECT")
	}
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return file.Chmod(mode)
}

// OpenPrivateFile refuses symlinks, special files and shared hardlinks before any data write.
func OpenPrivateFile(path string, flags int) (*os.File, error) {
	if flags&os.O_TRUNC != 0 {
		return nil, errors.New("UNSAFE_RUNTIME_TRUNCATE")
	}
	file, err := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	if err = privateObject(file, false); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// PrepareRuntime follows only DSM's package VAR alias; all owned child entries are no-follow.
func PrepareRuntime(state string) error {
	if !filepath.IsAbs(state) || filepath.Clean(state) != state || state == "/" {
		return errors.New("INVALID_RUNTIME_PATH")
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	directory, err := os.Open(state)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err = privateObject(directory, true); err != nil {
		return err
	}
	for _, name := range []string{"run", "staging", "generations", "logs"} {
		path := filepath.Join(state, name)
		if err = os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		child, e := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if e != nil {
			return e
		}
		e = privateObject(child, true)
		child.Close()
		if e != nil {
			return e
		}
	}
	// Existing databases and sidecars must be regular private files; never truncate them.
	for _, name := range []string{"control.db", "control.db-journal", "control.db-wal", "control.db-shm", "daemon.lock", "service.log", "diskmapd.pid"} {
		file, e := OpenPrivateFile(filepath.Join(state, name), os.O_RDWR)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		file.Close()
	}
	return nil
}
