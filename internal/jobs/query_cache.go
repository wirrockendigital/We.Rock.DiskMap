package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

// Reconstructible query data has a separate, bounded namespace from immutable indexes.
const queryCacheBudget int64 = 512 << 20
const queryCacheEntryLimit int64 = 128 << 20

// validQueryCacheKey restricts every relative filename to the internal SHA-256 namespace.
func validQueryCacheKey(key string) bool {
	if len(key) != 64 || key != strings.ToLower(key) {
		return false
	}
	_, err := hex.DecodeString(key)
	return err == nil
}

// querySourceKey binds derived data to the actual held SQLite descriptor, not a replaceable path.
func (m *Manager) querySourceKey(ctx context.Context, db *sql.DB, snapshot, kind string) string {
	if ctx.Err() != nil {
		return ""
	}
	source, ok := m.generationFiles.Load(db)
	if !ok {
		return ""
	}
	file, ok := source.(*os.File)
	if !ok {
		return ""
	}
	info, err := file.Stat()
	if err != nil || retainedStamp(info) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("query-v1:" + strconv.Itoa(index.GenerationSchema) + ":" + strconv.Itoa(index.AggregateVersion) + ":" + kind + ":" + snapshot + ":" + retainedStamp(info)))
	return hex.EncodeToString(sum[:])
}

// openQueryCacheDirectory refuses foreign permissions, links, and directory substitutions.
func openQueryCacheDirectory(state string) (*os.File, error) {
	root, _, err := scanner.OpenBoundRoot(state)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err = root.Mkdir("query-cache", 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	before, err := root.Lstat("query-cache")
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		return nil, ErrUnavailable
	}
	dir, err := root.Open("query-cache")
	if err != nil {
		return nil, err
	}
	after, err := dir.Stat()
	if err != nil || !os.SameFile(before, after) || !privateFilterFile(dir, true) {
		dir.Close()
		return nil, ErrUnavailable
	}
	return dir, nil
}

// queryCacheFile opens only a private regular inode and never waits on a substituted FIFO.
func queryCacheFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if !privateFilterFile(f, false) {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

// queryCacheIdentity qualifies the exact directory entry before replacing or removing it.
func queryCacheIdentity(dir, f *os.File, name string) bool {
	var held, current unix.Stat_t
	return privateFilterFile(f, false) && unix.Fstat(int(f.Fd()), &held) == nil && unix.Fstatat(int(dir.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) == nil && held.Dev == current.Dev && held.Ino == current.Ino && current.Mode&unix.S_IFMT == unix.S_IFREG
}

// readQueryCache checks length and checksum before decoding; failed caches remain disposable.
func (m *Manager) readQueryCache(key string) []byte {
	if !validQueryCacheKey(key) {
		return nil
	}
	dir, err := openQueryCacheDirectory(m.state)
	if err != nil {
		return nil
	}
	defer dir.Close()
	f, err := queryCacheFile(dir, key+".bin", unix.O_RDONLY)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() < 32 || info.Size() > queryCacheEntryLimit {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, queryCacheEntryLimit+1))
	if err != nil || int64(len(raw)) != info.Size() {
		return nil
	}
	data := raw[32:]
	sum := sha256.Sum256(data)
	if !queryCacheIdentity(dir, f, key+".bin") || !bytes.Equal(raw[:32], sum[:]) {
		return nil
	}
	return data
}

// writeQueryCache serializes atomic cache writes and evicts only qualified package-owned files.
// Storage failure never invalidates a successful read or modifies the source generation.
func (m *Manager) writeQueryCache(key string, data []byte) {
	if !validQueryCacheKey(key) || int64(len(data)+32) > queryCacheEntryLimit {
		return
	}
	// Preserve the same operational reserve used by native index admission.
	free, err := m.availableStateBytes()
	if err != nil || free < IndexReserveBytes+uint64(len(data))+32 {
		return
	}
	m.queryCacheMu.Lock()
	defer m.queryCacheMu.Unlock()
	dir, err := openQueryCacheDirectory(m.state)
	if err != nil {
		return
	}
	defer dir.Close()
	names, err := dir.Readdirnames(130)
	if err != nil && !errors.Is(err, io.EOF) || len(names) > 128 {
		return
	}
	type owned struct {
		name string
		f    *os.File
		info os.FileInfo
	}
	files := []owned{}
	total := int64(0)
	defer func() {
		for _, v := range files {
			v.f.Close()
		}
	}()
	for _, name := range names {
		suffix := strings.TrimSuffix(strings.TrimSuffix(name, ".bin"), ".tmp")
		if len(suffix) != 64 || (name != suffix+".bin" && name != suffix+".tmp") {
			return
		}
		if _, err = hex.DecodeString(suffix); err != nil {
			return
		}
		f, e := queryCacheFile(dir, name, unix.O_RDONLY)
		if e != nil {
			return
		}
		info, e := f.Stat()
		if e != nil {
			f.Close()
			return
		}
		files = append(files, owned{name, f, info})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].info.ModTime().Before(files[j].info.ModTime()) })
	// Leave room for an atomic replacement alongside the complete prior entry.
	count := len(files)
	for _, v := range files {
		if total+int64(len(data)+32) <= queryCacheBudget && count < 128 && v.name != key+".tmp" {
			continue
		}
		if !queryCacheIdentity(dir, v.f, v.name) || unix.Unlinkat(int(dir.Fd()), v.name, 0) != nil {
			return
		}
		total -= v.info.Size()
		count--
	}
	tmp, err := queryCacheFile(dir, key+".tmp", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return
	}
	defer tmp.Close()
	sum := sha256.Sum256(data)
	_, err = tmp.Write(sum[:])
	if err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if err != nil || !queryCacheIdentity(dir, tmp, key+".tmp") {
		return
	}
	// Existing destinations must also be owned regular files; no unknown entry is overwritten.
	old, e := queryCacheFile(dir, key+".bin", unix.O_RDONLY)
	if e == nil {
		defer old.Close()
		if !queryCacheIdentity(dir, old, key+".bin") {
			return
		}
	} else if !errors.Is(e, unix.ENOENT) {
		return
	}
	if unix.Renameat(int(dir.Fd()), key+".tmp", int(dir.Fd()), key+".bin") == nil {
		_ = dir.Sync()
	}
}
