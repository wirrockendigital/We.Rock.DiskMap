package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestActualTraversal100k is opt-in because it creates real local files, never synthetic metadata rows.
// Set DISKMAP_SCANNER_SCALE=1; the temporary directory must be local SSD storage, not a NAS mount.
func TestActualTraversal100k(t *testing.T) {
	if os.Getenv("DISKMAP_SCANNER_SCALE") != "1" {
		t.Skip("set DISKMAP_SCANNER_SCALE=1 for real 100k traversal")
	}
	for _, shape := range []string{"wide", "deep"} {
		t.Run(shape, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(base, "source")
			if err = os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			const files = 100000
			directories := 1
			current := source
			for i := 0; i < files; i++ {
				if shape == "deep" && i%500 == 0 && i > 0 {
					current = filepath.Join(current, "d")
					if err = os.Mkdir(current, 0700); err != nil {
						t.Fatal(err)
					}
					directories++
				}
				f, e := os.OpenFile(filepath.Join(current, fmt.Sprintf("f%06d", i)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				f.Close()
			}
			// A sibling sentinel proves the selected root never expands to a neighboring directory.
			if err = os.Mkdir(filepath.Join(base, "outside"), 0700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(base, "outside", "must-not-index"), nil, 0600)
			root, binding, err := OpenBoundRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			dir, _ := root.Open(".")
			maxStack, maxEntries, count := 0, 0, 0
			ops := walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }, observe: func(depth, entries int) {
				if depth > maxStack {
					maxStack = depth
				}
				if entries > maxEntries {
					maxEntries = entries
				}
			}}
			traversalStart := time.Now()
			err = walkMetadata(context.Background(), dir, ".", binding.MountID, func(_ string, _ os.FileInfo, e error) error {
				if e != nil {
					return e
				}
				count++
				return nil
			}, ops)
			dir.Close()
			root.Close()
			if err != nil || count != files+directories || maxStack > TraversalMaxDepth || maxEntries > TraversalMaxDepth*TraversalBatchSize {
				t.Fatal(err, count, maxStack, maxEntries)
			}
			traversalMS := time.Since(traversalStart).Milliseconds()
			db, err := index.Open(filepath.Join(base, "index.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			runtime.GC()
			baselineFDs := processFDCount(t)
			var heapMax, fdMax atomic.Uint64
			stop, stopped := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(stopped)
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var mem runtime.MemStats
					runtime.ReadMemStats(&mem)
					if mem.HeapAlloc > heapMax.Load() {
						heapMax.Store(mem.HeapAlloc)
					}
					fdPath := "/proc/self/fd"
					if runtime.GOOS == "darwin" {
						fdPath = "/dev/fd"
					}
					if entries, e := os.ReadDir(fdPath); e == nil && uint64(len(entries)) > fdMax.Load() {
						fdMax.Store(uint64(len(entries)))
					}
					select {
					case <-stop:
						return
					case <-ticker.C:
					}
				}
			}()
			scanStart := time.Now()
			p := contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}}
			result, err := Scan(context.Background(), db, "scale-"+shape, p, map[string]RootBinding{"share": binding}, map[string]string{"share": "volume"})
			close(stop)
			<-stopped
			if err != nil || result.Errors != 0 || result.Entries != uint64(files+directories) || result.Bytes != "0" {
				t.Fatal(result, err)
			}
			var nodes int
			if e := db.QueryRow(`SELECT count(*) FROM nodes`).Scan(&nodes); e != nil || nodes != files+directories {
				t.Fatal(nodes, e)
			}
			var usage syscall.Rusage
			if err = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
				t.Fatal(err)
			}
			rss := uint64(usage.Maxrss)
			if runtime.GOOS != "darwin" {
				rss *= 1024
			}
			t.Logf("shape=%s real_files=%d directories=%d traversal_ms=%d scan_ms=%d max_stack=%d max_retained_entries=%d sampled_heap_bytes=%d sampled_process_fds=%d kernel_peak_rss_bytes=%d", shape, files, directories, traversalMS, time.Since(scanStart).Milliseconds(), maxStack, maxEntries, heapMax.Load(), fdMax.Load(), rss)
			directoryFDMultiplier := 1
			if runtime.GOOS == "darwin" {
				directoryFDMultiplier = 2
			} // Go fdopendir owns a duplicated descriptor on Darwin.
			if fdMax.Load() > uint64(directoryFDMultiplier*TraversalMaxDepth+32) {
				t.Fatal("unexpected descriptor growth", fdMax.Load())
			}
			if remaining := processFDCount(t); remaining > baselineFDs+2 {
				t.Fatal("descriptors retained after scan", baselineFDs, remaining)
			}
			if heapMax.Load() > 256<<20 || rss > 512<<20 {
				t.Fatal("100k traversal memory exceeded test guard", heapMax.Load(), rss)
			}
		})
	}
}

// processFDCount samples the complete test process, including Go/SQLite runtime descriptors.
func processFDCount(t *testing.T) int {
	t.Helper()
	name := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		name = "/dev/fd"
	}
	entries, err := os.ReadDir(name)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
