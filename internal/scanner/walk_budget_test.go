package scanner

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// TestIterativeWalkExclusionsAndSpecialFiles audits actual production operations, not a fake tree.
func TestIterativeWalkExclusionsAndSpecialFiles(t *testing.T) {
	short, err := os.MkdirTemp("/tmp", "dm-meta-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	base, err := filepath.EvalSymlinks(short)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(base, "excluded"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "file"), []byte("untouched"), 0000); err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(filepath.Join(base, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(base, "socket"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err = os.Symlink("../outside", filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	root, binding, err := OpenBoundRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	visited := map[string]bool{}
	opened := 0
	ops := walkOperations{inspect: func(p *os.File, n, m string) (*os.File, os.FileInfo, error) {
		f, i, e := inspectEntry(p, n, m)
		if e == nil && runtime.GOOS == "linux" && f != nil {
			var b [1]byte
			if _, readErr := f.Read(b[:]); readErr == nil || readErr == io.EOF {
				t.Error("metadata-only handle read content")
			}
		}
		return f, i, e
	}, directory: func(p *os.File, n string, i os.FileInfo, m string) (*os.File, error) {
		opened++
		return openDirectory(p, n, i, m)
	}, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
		if n != TraversalBatchSize {
			t.Fatal(n)
		}
		return f.ReadDir(n)
	}}
	err = walkMetadata(context.Background(), dir, ".", binding.MountID, func(rel string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if rel == "excluded" {
			return fs.SkipDir
		}
		visited[rel] = true
		return nil
	}, ops)
	if err != nil || opened != 0 || len(visited) != 5 || !visited["fifo"] || !visited["socket"] || !visited["link"] {
		t.Fatal(err, opened, visited)
	}
}

// TestIterativeWalkClosesAcquiredDescriptors checks early visitor and cancellation exits.
func TestIterativeWalkClosesAcquiredDescriptors(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "visitor", true: "cancel"}[cancelled], func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(filepath.Join(base, "child"), 0700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(base, "child", "leaf"), nil, 0600)
			root, binding, err := OpenBoundRoot(base)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			dir, _ := root.Open(".")
			defer dir.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("visitor stop")
			var acquired []*os.File
			ops := walkOperations{inspect: inspectEntry, directory: func(p *os.File, n string, i os.FileInfo, m string) (*os.File, error) {
				f, e := openDirectory(p, n, i, m)
				if e == nil {
					acquired = append(acquired, f)
				}
				return f, e
			}, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }}
			err = walkMetadata(ctx, dir, ".", binding.MountID, func(rel string, _ os.FileInfo, e error) error {
				if e != nil {
					return e
				}
				if rel == "child/leaf" {
					if cancelled {
						cancel()
						return nil
					}
					return sentinel
				}
				return nil
			}, ops)
			if cancelled && !errors.Is(err, context.Canceled) || !cancelled && !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if len(acquired) != 1 {
				t.Fatal("child not acquired")
			}
			for _, f := range acquired {
				if _, e := f.Stat(); !errors.Is(e, os.ErrClosed) {
					t.Fatal("child descriptor leaked", e)
				}
			}
			if _, e := dir.Stat(); e != nil {
				t.Fatal("caller-owned root closed", e)
			}
		})
	}
}

// TestMetadataBytePrecision includes sparse, unknown and values whose block product exceeds int64.
func TestMetadataBytePrecision(t *testing.T) {
	for _, tc := range []struct {
		size, blocks       int64
		logical, allocated string
	}{
		{0, 0, "0", "0"}, {9007199254740993, 1, "9007199254740993", "512"}, {1 << 40, 0, "1099511627776", "0"}, {-1, -1, "", ""}, {1, 9223372036854775807, "1", "4722366482869645213184"},
	} {
		a, b := metadataBytes(tc.size, tc.blocks)
		if tc.logical == "" {
			if a != nil {
				t.Fatal(a)
			}
		} else if a == nil || string(*a) != tc.logical {
			t.Fatal(a)
		}
		if tc.allocated == "" {
			if b != nil {
				t.Fatal(b)
			}
		} else if b == nil || string(*b) != tc.allocated {
			t.Fatal(b)
		}
	}
}
