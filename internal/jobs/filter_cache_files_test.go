package jobs

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

// TestFilterCacheRestartDiscardsOnlyQualifiedInodes never trusts a persisted complete marker after restart.
func TestFilterCacheRestartDiscardsOnlyQualifiedInodes(t *testing.T) {
	m, h, _ := cacheFixture(t)
	_, release := waitCache(t, m, h, query.Filter{})
	release()
	path := m.state
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	dir, err := openFilterCacheDirectory(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	if err = discardPriorFilterCaches(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "filter-cache"))
	if err != nil || len(entries) != 0 {
		t.Fatal(entries, err)
	}
}

// TestFilterCacheUnsafeFilesystem rejects aliases without modifying their targets.
func TestFilterCacheUnsafeFilesystem(t *testing.T) {
	for _, kind := range []string{"directory-link", "file-link", "hardlink", "unowned", "copied-owner"} {
		t.Run(kind, func(t *testing.T) {
			state, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(state, "keep")
			if err = os.WriteFile(target, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "directory-link" {
				if err = os.Symlink(state, filepath.Join(state, "filter-cache")); err != nil {
					t.Fatal(err)
				}
				if dir, e := openFilterCacheDirectory(state); e == nil {
					dir.Close()
					t.Fatal("directory symlink accepted")
				}
				return
			}
			dir, err := openFilterCacheDirectory(state)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			key := strings.Repeat("a", 64)
			cache := filepath.Join(state, "filter-cache", key+".db")
			switch kind {
			case "file-link":
				err = os.Symlink(target, cache)
			case "hardlink":
				err = os.Link(target, cache)
			case "unowned":
				err = os.WriteFile(cache, []byte("unknown"), 0600)
			case "copied-owner":
				var f *os.File
				f, err = createFilterCacheFile(dir, key)
				if err != nil {
					t.Fatal(err)
				}
				db, e := filterCacheDB(f, false)
				if e != nil {
					t.Fatal(e)
				}
				_, err = db.Exec(`PRAGMA journal_mode=MEMORY; CREATE TABLE filter_cache_owner(version,key,snapshot,object); INSERT INTO filter_cache_owner VALUES(1,?,'snapshot','not-this-inode')`, key)
				db.Close()
				f.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = discardPriorFilterCaches(context.Background(), dir); err == nil {
				t.Fatal("unsafe cache accepted")
			}
			got, err := os.ReadFile(target)
			if err != nil || string(got) != "preserve" {
				t.Fatal("target modified", err)
			}
			if _, err = os.Lstat(cache); err != nil {
				t.Fatal("unqualified entry removed", err)
			}
		})
	}
}

// TestFilterCacheCrashRebuildUsesDurableOwnership accepts a destroyed owned DB, not an exchanged inode.
func TestFilterCacheCrashRebuildUsesDurableOwnership(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "damaged-own-inode", true: "foreign-replacement"}[replace], func(t *testing.T) {
			m, h, _ := cacheFixture(t)
			_, release := waitCache(t, m, h, query.Filter{})
			release()
			key, _ := query.FilterProjectionKey(h, "logical", query.Filter{})
			state := m.state
			path := filepath.Join(state, "filter-cache", key+".db")
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			if replace {
				if err := os.Rename(path, path+".saved"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("interrupted SQLite write"), 0600); err != nil {
				t.Fatal(err)
			}
			// The saved replacement stays outside the cache inventory so this branch tests inode binding itself.
			if replace {
				if err := os.Rename(path+".saved", filepath.Join(state, "saved-owned-cache")); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := openFilterCacheDirectory(state)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			err = discardPriorFilterCaches(context.Background(), dir)
			if replace {
				if err == nil {
					t.Fatal("foreign inode deleted")
				}
				if _, e := os.Stat(path); e != nil {
					t.Fatal(e)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, e := os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("owned corrupt cache remains", e)
				}
			}
		})
	}
}

// TestFilterCacheV2RejectsChangedOwnership leaves every unqualified database untouched.
func TestFilterCacheV2RejectsChangedOwnership(t *testing.T) {
	for _, kind := range []string{"v1", "filesystem", "subvolume", "inode", "corrupt-marker", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			m, h, _ := cacheFixture(t)
			_, release := waitCache(t, m, h, query.Filter{})
			release()
			key, _ := query.FilterProjectionKey(h, "logical", query.Filter{})
			state := m.state
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "filter-cache", key+".db")
			marker := filepath.Join(state, "filter-cache", key+".owner")
			raw, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			var owner filterCacheOwner
			if err = json.Unmarshal(raw, &owner); err != nil {
				t.Fatal(err)
			}
			if owner.Version != 2 {
				t.Fatalf("wrong marker version: %d", owner.Version)
			}
			parts := strings.Split(owner.Object, ":")
			switch kind {
			case "v1":
				owner.Version = 1
				owner.Object = "37:41738"
			case "filesystem":
				if parts[1][0] == 'a' {
					parts[1] = "b" + parts[1][1:]
				} else {
					parts[1] = "a" + parts[1][1:]
				}
				owner.Object = strings.Join(parts, ":")
			case "subvolume":
				owner.Object = "btrfs:11111111111111111111111111111111:999:" + parts[len(parts)-1]
			case "inode":
				parts[len(parts)-1] = "18446744073709551615"
				owner.Object = strings.Join(parts, ":")
			case "hardlink":
				if err = os.Link(path, filepath.Join(state, "retained-alias")); err != nil {
					t.Fatal(err)
				}
			}
			raw, err = json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "corrupt-marker" {
				raw = []byte("interrupted ownership write")
			}
			if err = os.WriteFile(marker, raw, 0600); err != nil {
				t.Fatal(err)
			}
			dir, err := openFilterCacheDirectory(state)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			if err = discardPriorFilterCaches(context.Background(), dir); err == nil {
				t.Fatal("changed ownership accepted")
			}
			if _, err = os.Lstat(path); err != nil {
				t.Fatal("unqualified database removed", err)
			}
			if _, err = os.Lstat(marker); err != nil {
				t.Fatal("unqualified marker removed", err)
			}
		})
	}
}

// TestFilterCachePersistentIdentityIgnoresDevice models the observed DSM reboot without weakening runtime checks.
func TestFilterCachePersistentIdentityIgnoresDevice(t *testing.T) {
	before := unix.Stat_t{Dev: 37, Ino: 41738}
	after := unix.Stat_t{Dev: 44, Ino: 41738}
	filesystem := "btrfs:11111111111111111111111111111111:256"
	want := persistentFilterObject(filesystem, before)
	if want == "" || persistentFilterObject(filesystem, after) != want {
		t.Fatal("ephemeral device number entered persistent identity")
	}
	after.Ino++
	if persistentFilterObject(filesystem, after) == want {
		t.Fatal("different inode accepted")
	}
	for _, fs := range []string{"btrfs:22222222222222222222222222222222:256", "btrfs:11111111111111111111111111111111:257"} {
		if persistentFilterObject(fs, before) == want {
			t.Fatal("different filesystem or subvolume accepted")
		}
	}
	for _, object := range []string{"37:41738", "btrfs:00000000000000000000000000000000:256:41738", "btrfs:11111111111111111111111111111111:0:41738", "btrfs:11111111111111111111111111111111:256:0", "darwin-boot:11111111111111111111111111111111:0000000000000000:1"} {
		if validFilterCacheObject(object) {
			t.Fatalf("invalid identity accepted: %s", object)
		}
	}
}

// TestFilterCacheFIFOReplacement rejects named pipes without waiting for an absent writer.
func TestFilterCacheFIFOReplacement(t *testing.T) {
	for _, suffix := range []string{".db", ".owner"} {
		t.Run(suffix, func(t *testing.T) {
			m, h, _ := cacheFixture(t)
			_, release := waitCache(t, m, h, query.Filter{})
			release()
			key, _ := query.FilterProjectionKey(h, "logical", query.Filter{})
			state := m.state
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, "filter-cache", key+suffix)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mkfifo(path, 0600); err != nil {
				t.Fatal(err)
			}
			dir, err := openFilterCacheDirectory(state)
			if err != nil {
				t.Fatal(err)
			}
			defer dir.Close()
			result := make(chan error, 1)
			go func() { result <- discardPriorFilterCaches(context.Background(), dir) }()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("FIFO accepted")
				}
			case <-time.After(time.Second):
				// Unblock a regressed blocking open so the failed test cannot leak a goroutine.
				fd, e := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
				if e == nil {
					unix.Close(fd)
				}
				t.Fatal("ownership validation blocked on FIFO")
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
				t.Fatal("unqualified FIFO changed", err)
			}
		})
	}
}
