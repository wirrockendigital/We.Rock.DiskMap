package jobs

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"golang.org/x/sys/unix"
)

// A real publication prepares exact derived results that survive a fresh Manager and stale source stamps.
func TestPublishedQueryCacheRestartAndSourceBinding(t *testing.T) {
	m, in, catalog := bootFixture(t)
	source := catalog.Shares[0].PhysicalPath
	if err := os.Mkdir(filepath.Join(source, "#recycle"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "#recycle", "deleted"), []byte("trash"), 0600); err != nil {
		t.Fatal(err)
	}
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, started.Job.ID)
	if done.Job.State != "completed" {
		t.Fatal(done.Job.State)
	}
	snapshot := *done.Job.ResultSnapshotID
	db, release, err := m.generationDB(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	key := m.querySourceKey(context.Background(), db, snapshot, "map")
	if key == "" {
		t.Fatal("held descriptor did not produce a source key")
	}
	raw := m.readQueryCache(key)
	if raw == nil {
		t.Fatal("publication omitted map cache")
	}
	got, err := query.DecodeMapCategories(raw)
	if err != nil {
		t.Fatal(err)
	}
	want, err := query.BuildMapCategories(context.Background(), db)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("cached totals changed", err)
	}
	roots, ok := m.persistedRecycleObservations(context.Background(), db, snapshot)
	if !ok || len(roots) != 1 || roots[0].bytes != "5" {
		t.Fatal("missing recycle preparation", roots)
	}
	release()
	// Recreate only the cache consumer; no in-memory result can satisfy this read.
	fresh := &Manager{state: m.state}
	if !reflect.DeepEqual(raw, fresh.readQueryCache(key)) {
		t.Fatal("cache did not survive a fresh consumer")
	}
	indexPath := filepath.Join(m.state, "generations", snapshot, "index.db")
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime().Add(time.Second)
	if err = os.Chtimes(indexPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// A held descriptor sees the changed metadata and cannot reuse the older derived result.
	db, release, err = m.generationDB(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	newKey := m.querySourceKey(context.Background(), db, snapshot, "map")
	if newKey == key || fresh.readQueryCache(newKey) != nil {
		t.Fatal("stale source identity reused")
	}
}

// Corrupt bytes are discarded; links, FIFOs and unknown directory entries never become write targets.
func TestQueryCacheRejectsCorruptionAndAliases(t *testing.T) {
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	m := &Manager{state: state}
	key := strings.Repeat("a", 64)
	m.writeQueryCache(key, []byte("exact"))
	path := filepath.Join(state, "query-cache", key+".bin")
	if string(m.readQueryCache(key)) != "exact" {
		t.Fatal("cache not written")
	}
	// Invalid path-shaped keys cannot escape the dedicated cache directory.
	m.writeQueryCache(strings.Repeat("../", 21)+"x", []byte("invalid"))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if m.readQueryCache(key) != nil {
		t.Fatal("corrupt data trusted")
	}
	m.writeQueryCache(key, []byte("repaired"))
	if string(m.readQueryCache(key)) != "repaired" {
		t.Fatal("corrupt cache not reconstructed")
	}
	for _, kind := range []string{"symlink", "hardlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			os.Chmod(dir, 0700)
			consumer := &Manager{state: dir}
			cache, err := openQueryCacheDirectory(dir)
			if err != nil {
				t.Fatal(err)
			}
			cache.Close()
			victim := filepath.Join(dir, "victim")
			os.WriteFile(victim, []byte("preserved"), 0600)
			entry := filepath.Join(dir, "query-cache", key+".bin")
			switch kind {
			case "symlink":
				err = os.Symlink(victim, entry)
			case "hardlink":
				err = os.Link(victim, entry)
			case "fifo":
				err = unix.Mkfifo(entry, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if consumer.readQueryCache(key) != nil {
				t.Fatal("alias trusted")
			}
			consumer.writeQueryCache(key, []byte("replacement"))
			data, _ := os.ReadFile(victim)
			if string(data) != "preserved" {
				t.Fatal("foreign inode overwritten")
			}
		})
	}
}

// The fixed-record decoder refuses adversarial lengths and inconsistent ancestry before allocation.
func TestMapCatalogCodecRejectsInvalidDimensions(t *testing.T) {
	for _, raw := range [][]byte{nil, make([]byte, 7), func() []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, 2000001); return b }()} {
		if _, err := query.DecodeMapCategories(raw); err == nil {
			t.Fatal("invalid dimensions accepted")
		}
	}
	c := &query.MapCategories{Dirs: map[int64]*query.MapDirectory{1: {}, 2: {Parent: 1, Flags: 2, Bytes: [4]uint64{0, 0, 5, 0}, Files: 1}}}
	raw := query.EncodeMapCategories(c)
	decoded, err := query.DecodeMapCategories(raw)
	if err != nil || !reflect.DeepEqual(c, decoded) {
		t.Fatal("roundtrip", err)
	}
	raw[8+64+8] = 3
	if _, err = query.DecodeMapCategories(raw); err == nil {
		t.Fatal("forward parent accepted")
	}
}

// A cache at the inventory bound evicts only qualified entries and leaves the file count bounded.
func TestQueryCacheInventoryBound(t *testing.T) {
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(state, 0700)
	m := &Manager{state: state}
	for i := 0; i < 132; i++ {
		var key [32]byte
		binary.LittleEndian.PutUint64(key[:], uint64(i))
		m.writeQueryCache(hex.EncodeToString(key[:]), []byte("x"))
	}
	entries, err := os.ReadDir(filepath.Join(state, "query-cache"))
	if err != nil || len(entries) > 128 {
		t.Fatal("unbounded cache", len(entries), err)
	}
}

// Sparse owned cache files exercise the disk budget without allocating hundreds of MB in a test.
func TestQueryCacheDiskBudgetAndReserve(t *testing.T) {
	state, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(state, 0700)
	m := &Manager{state: state}
	dir, err := openQueryCacheDirectory(state)
	if err != nil {
		t.Fatal(err)
	}
	dir.Close()
	for i := 0; i < 4; i++ {
		name := strings.Repeat(string(rune('a'+i)), 64) + ".bin"
		f, err := os.OpenFile(filepath.Join(state, "query-cache", name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(queryCacheEntryLimit); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
	key := strings.Repeat("e", 64)
	m.writeQueryCache(key, []byte("exact"))
	entries, err := os.ReadDir(filepath.Join(state, "query-cache"))
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
	}
	if total > queryCacheBudget || string(m.readQueryCache(key)) != "exact" {
		t.Fatal("disk quota not enforced", total)
	}
	m.spaceProbe = func() (uint64, error) { return IndexReserveBytes, nil }
	m.writeQueryCache(strings.Repeat("f", 64), []byte("must not consume reserve"))
	if m.readQueryCache(strings.Repeat("f", 64)) != nil {
		t.Fatal("operational reserve consumed")
	}
}
