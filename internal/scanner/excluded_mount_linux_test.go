//go:build linux

package scanner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A deliberately different expected mount exercises the real O_PATH/fstat error transport without mounting anything.
func TestRejectedMountRetainsNoFollowType(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("directory", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	for _, name := range []string{"directory", "link"} {
		handle, info, err := inspectEntry(parent, name, "deliberately-different-mount")
		if handle != nil {
			handle.Close()
			t.Fatal("rejected mount retained a handle")
		}
		if !errors.Is(err, errMountChanged) || info == nil {
			t.Fatalf("metadata missing for %s: %v", name, err)
		}
		if info.IsDir() != (name == "directory") || ((info.Mode()&os.ModeSymlink) != 0) != (name == "link") {
			t.Fatal("no-follow type changed")
		}
	}
	_, info, err := inspectEntry(parent, "missing", "deliberately-different-mount")
	if !errors.Is(err, os.ErrNotExist) || info != nil {
		t.Fatal("invented metadata for absent source")
	}
}
