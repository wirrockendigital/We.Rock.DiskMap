package scanner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Instrument actual production boundaries: unreadable contents still yield metadata, never file reads.
func TestMetadataOnlyOperations(t *testing.T) {
	// Resolve the host temporary-directory alias before the production no-follow gate.
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(source, "nested"), 0700)
	os.WriteFile(filepath.Join(source, "nested", "private.bin"), []byte("must not read"), 0000)
	os.Symlink("nested/private.bin", filepath.Join(source, "link"))
	root, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	inspections, enumerations, openedDirectories := 0, 0, 0
	ops := walkOperations{
		inspect: func(parent *os.File, name, mount string) (*os.File, os.FileInfo, error) {
			inspections++
			handle, info, err := inspectEntry(parent, name, mount)
			if err == nil && handle != nil && runtime.GOOS == "linux" {
				b := make([]byte, 1)
				if _, e := handle.Read(b); e == nil || e == io.EOF {
					t.Error("metadata handle permitted contents")
				}
			}
			return handle, info, err
		},
		directory: func(parent *os.File, name string, info os.FileInfo, mount string) (*os.File, error) {
			openedDirectories++
			if !info.IsDir() {
				t.Error("opened a non-directory")
			}
			return openDirectory(parent, name, info, mount)
		},
		readDirectory: func(file *os.File, n int) ([]os.DirEntry, error) {
			enumerations++
			info, err := file.Stat()
			if err != nil || !info.IsDir() {
				t.Fatal("enumeration is not a directory")
			}
			return file.ReadDir(n)
		},
	}
	visited := map[string]bool{}
	err = walkMetadata(context.Background(), directory, ".", binding.MountID, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		visited[path] = true
		return nil
	}, ops)
	if err != nil {
		t.Fatal(err)
	}
	if inspections != 3 || openedDirectories != 1 || enumerations < 2 || len(visited) != 4 || !visited["nested/private.bin"] {
		t.Fatalf("unexpected calls %d/%d/%d %#v", inspections, openedDirectories, enumerations, visited)
	}
	info, err := os.Lstat(filepath.Join(source, "nested", "private.bin"))
	if err != nil || info.Mode().Perm() != 0 || info.Size() != 13 {
		t.Fatalf("source metadata changed: %v %v", info, err)
	}
}
