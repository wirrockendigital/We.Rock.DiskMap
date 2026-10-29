package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTraverseOnlyAncestors requires only search rights above the readable scan root.
func TestTraverseOnlyAncestors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged execution to prove directory permission semantics")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(base, "search-only")
	source := filepath.Join(ancestor, "readable-root")
	if err = os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "fixture"), []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(ancestor, 0100); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ancestor, 0700)
	if _, err = os.ReadDir(ancestor); !os.IsPermission(err) {
		t.Fatalf("fixture does not enforce search-only permissions: %v", err)
	}
	root, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatalf("readable root behind search-only ancestor: %v", err)
	}
	if _, err = root.Stat("fixture"); err != nil {
		root.Close()
		t.Fatal(err)
	}
	root.Close()
	verified, err := openVerifiedRoot(binding)
	if err != nil {
		t.Fatal(err)
	}
	verified.Close()
	// A scan root itself still needs listing permission for real metadata traversal.
	if err = os.Chmod(source, 0100); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(source, 0700)
	unreadable, _, err := OpenBoundRoot(source)
	if err == nil {
		unreadable.Close()
		t.Fatal("unreadable final root accepted")
	}
}
