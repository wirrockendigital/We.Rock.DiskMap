package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

var filterCacheName = regexp.MustCompile(`^[a-f0-9]{64}\.db$`)

// openFilterCacheDirectory pins a private directory reached through the existing bound package state.
func openFilterCacheDirectory(state string) (*os.File, error) {
	root, _, err := scanner.OpenBoundRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = root.Mkdir("filter-cache", 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	before, err := root.Lstat("filter-cache")
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		return nil, ErrFilterUnavailable
	}
	dir, err := root.Open("filter-cache")
	if err != nil {
		return nil, err
	}
	after, err := dir.Stat()
	if err != nil || !os.SameFile(before, after) || !privateFilterFile(dir, true) {
		dir.Close()
		return nil, ErrFilterUnavailable
	}
	return dir, nil
}

// privateFilterFile rejects shared, aliased, or foreign cache objects before SQLite or unlink sees them.
func privateFilterFile(f *os.File, directory bool) bool {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil || st.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Mode&0777 == 0700
	}
	return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Mode&0777 == 0600 && st.Nlink == 1
}

// createFilterCacheFile never overwrites a pre-existing directory entry.
func createFilterCacheFile(dir *os.File, key string) (*os.File, error) {
	if !filterCacheName.MatchString(key+".db") || !privateFilterFile(dir, true) {
		return nil, ErrFilterUnavailable
	}
	fd, err := unix.Openat(int(dir.Fd()), key+".db", unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), key+".db"), nil
}

// filterCacheIdentity checks the held inode and current directory entry without following a replacement link.
func filterCacheIdentity(dir, file *os.File, key, stamp string) bool {
	if dir == nil || file == nil || !privateFilterFile(dir, true) || !privateFilterFile(file, false) {
		return false
	}
	var held, current unix.Stat_t
	if unix.Fstat(int(file.Fd()), &held) != nil || unix.Fstatat(int(dir.Fd()), key+".db", &current, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return false
	}
	if held.Dev != current.Dev || held.Ino != current.Ino || current.Mode&unix.S_IFMT != unix.S_IFREG {
		return false
	}
	if stamp != "" {
		info, err := file.Stat()
		return err == nil && retainedStamp(info) == stamp
	}
	return true
}

// filterCacheDB always addresses the held descriptor, never an exchangeable filesystem pathname.
func filterCacheDB(file *os.File, readonly bool) (*sql.DB, error) {
	if !privateFilterFile(file, false) {
		return nil, ErrFilterUnavailable
	}
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	uri := url.URL{Scheme: "file", Path: prefix + strconv.FormatUint(uint64(file.Fd()), 10)}
	opts := "?mode=rw"
	if readonly {
		opts = "?mode=ro&immutable=1"
	}
	db, err := sql.Open("sqlite", uri.String()+opts)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// setFilterCachePages uses only a computed integer, never a client-provided PRAGMA expression.
func setFilterCachePages(ctx context.Context, db *sql.DB, pages uint64) error {
	var applied uint64
	if err := db.QueryRowContext(ctx, "PRAGMA max_page_count="+strconv.FormatUint(pages, 10)).Scan(&applied); err != nil {
		return err
	}
	if applied > pages {
		return ErrFilterUnavailable
	}
	return nil
}

// evict removes only the exact private inode owned by this process and never follows foreign entries.
func (c *filterCacheManager) evict(e *filterCacheEntry) error {
	if e.refs != 0 || e == c.building {
		return ErrFilterUnavailable
	}
	if e.file != nil {
		if !filterCacheIdentity(c.dir, e.file, e.key, e.stamp) || !filterMarkerIdentity(c.dir, e.marker, e.key) {
			return ErrFilterUnavailable
		}
		if e.db != nil {
			if err := e.db.Close(); err != nil {
				return err
			}
			e.db = nil
		}
		if err := unix.Unlinkat(int(c.dir.Fd()), e.key+".db", 0); err != nil {
			return err
		}
		e.file.Close()
		e.file = nil
		if err := unix.Unlinkat(int(c.dir.Fd()), e.key+".owner", 0); err != nil {
			return err
		}
		e.marker.Close()
		e.marker = nil
	}
	delete(c.entries, e.key)
	return nil
}

// filterCacheOwner is synced separately before SQLite can leave a damaged reconstructible cache.
type filterCacheOwner struct {
	Version  int    `json:"version"`
	Key      string `json:"key"`
	Snapshot string `json:"snapshot"`
	Object   string `json:"object"`
}

// createFilterCacheMarker establishes durable ownership before the first database mutation.
func createFilterCacheMarker(dir *os.File, e *filterCacheEntry) (*os.File, error) {
	object := filterCacheObject(e.file)
	if object == "" {
		return nil, ErrFilterUnavailable
	}
	name := e.key + ".owner"
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	marker := os.NewFile(uintptr(fd), name)
	raw, err := json.Marshal(filterCacheOwner{2, e.key, e.header.SnapshotID, object})
	if err == nil {
		_, err = marker.Write(raw)
	}
	if err == nil {
		err = marker.Sync()
	}
	if err == nil {
		err = dir.Sync()
	}
	if err != nil {
		marker.Close()
		return nil, err
	}
	return marker, nil
}

// readFilterCacheMarker reads a fixed small ownership document without following aliases.
// Nonblocking opens allow the subsequent regular-file check to reject a substituted FIFO.
func readFilterCacheMarker(dir *os.File, key string) (*os.File, filterCacheOwner, error) {
	var owner filterCacheOwner
	fd, err := unix.Openat(int(dir.Fd()), key+".owner", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, owner, err
	}
	f := os.NewFile(uintptr(fd), key+".owner")
	fail := func() (*os.File, filterCacheOwner, error) { f.Close(); return nil, owner, ErrFilterUnavailable }
	if !privateFilterFile(f, false) {
		return fail()
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil || len(raw) > 1024 {
		return fail()
	}
	if json.Unmarshal(raw, &owner) != nil || owner.Version != 2 || owner.Key != key || !identifier.MatchString(owner.Snapshot) || !validFilterCacheObject(owner.Object) {
		return fail()
	}
	if !filterMarkerIdentity(dir, f, key) {
		return fail()
	}
	return f, owner, nil
}

// filterMarkerIdentity refuses to unlink an ownership marker replaced after qualification.
func filterMarkerIdentity(dir, f *os.File, key string) bool {
	if f == nil || !privateFilterFile(f, false) {
		return false
	}
	var held, current unix.Stat_t
	return unix.Fstat(int(f.Fd()), &held) == nil && unix.Fstatat(int(dir.Fd()), key+".owner", &current, unix.AT_SYMLINK_NOFOLLOW) == nil && held.Dev == current.Dev && held.Ino == current.Ino && current.Mode&unix.S_IFMT == unix.S_IFREG
}

// discardPriorFilterCaches validates the complete bounded inventory before deleting reconstructible files.
// The durable marker permits reconstruction even when a crash damaged every SQLite page.
func discardPriorFilterCaches(parent context.Context, dir *os.File) error {
	names, err := dir.Readdirnames(7)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(names) > 6 {
		return ErrFilterUnavailable
	}
	keys := map[string]bool{}
	for _, name := range names {
		if filepath.Base(name) != name {
			return ErrFilterUnavailable
		}
		key := strings.TrimSuffix(strings.TrimSuffix(name, ".owner"), ".db")
		if !filterCacheName.MatchString(key+".db") || (name != key+".db" && name != key+".owner") {
			return ErrFilterUnavailable
		}
		keys[key] = true
	}
	type heldPair struct {
		key        string
		db, marker *os.File
	}
	pairs := []heldPair{}
	defer func() {
		for _, p := range pairs {
			if p.db != nil {
				p.db.Close()
			}
			p.marker.Close()
		}
	}()
	for key := range keys {
		if parent.Err() != nil {
			return parent.Err()
		}
		marker, owner, e := readFilterCacheMarker(dir, key)
		if e != nil {
			return ErrFilterUnavailable
		}
		pair := heldPair{key: key, marker: marker}
		pairs = append(pairs, pair)
		// Reject special-file replacements before SQLite without waiting for a FIFO writer.
		fd, e := unix.Openat(int(dir.Fd()), key+".db", unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(e, unix.ENOENT) {
			continue
		} // A prior eviction may have removed the DB before its marker.
		if e != nil {
			return ErrFilterUnavailable
		}
		f := os.NewFile(uintptr(fd), key+".db")
		pairs[len(pairs)-1].db = f
		if !privateFilterFile(f, false) || owner.Object != filterCacheObject(f) || !filterCacheIdentity(dir, f, key, "") {
			return ErrFilterUnavailable
		}
	}
	for _, p := range pairs {
		if parent.Err() != nil {
			return parent.Err()
		}
		if !filterMarkerIdentity(dir, p.marker, p.key) {
			return ErrFilterUnavailable
		}
		if p.db != nil {
			if !filterCacheIdentity(dir, p.db, p.key, "") {
				return ErrFilterUnavailable
			}
			if err = unix.Unlinkat(int(dir.Fd()), p.key+".db", 0); err != nil {
				return err
			}
		}
		if err = unix.Unlinkat(int(dir.Fd()), p.key+".owner", 0); err != nil {
			return err
		}
	}
	return dir.Sync()
}
