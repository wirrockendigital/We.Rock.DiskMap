package security

import (
	"os"
	"path/filepath"
	"testing"
)

// Existing metadata survives repair of permissive modes, including the DSM VAR alias.
func TestPrivateRuntimePreservesData(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(state, "control.db")
	if err := os.WriteFile(db, []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(state), "var")
	if err := os.Symlink(state, alias); err != nil {
		t.Fatal(err)
	}
	if err := PrepareRuntime(alias); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "run", "staging", "generations", "logs"} {
		info, err := os.Stat(filepath.Join(state, name))
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("directory %s: %v %v", name, info, err)
		}
	}
	info, _ := os.Stat(db)
	data, _ := os.ReadFile(db)
	if info.Mode().Perm() != 0600 || string(data) != "preserved" {
		t.Fatal("data or mode changed incorrectly")
	}
}

// A crafted child cannot redirect chmod, logs or database access outside private state.
func TestPrivateRuntimeRejectsAliases(t *testing.T) {
	for _, kind := range []string{"symlink-file", "hardlink-file", "symlink-dir", "directory-file"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			state := filepath.Join(base, "state")
			os.Mkdir(state, 0700)
			outside := filepath.Join(base, "outside")
			os.WriteFile(outside, []byte("unchanged"), 0644)
			var err error
			switch kind {
			case "symlink-file":
				err = os.Symlink(outside, filepath.Join(state, "service.log"))
			case "hardlink-file":
				err = os.Link(outside, filepath.Join(state, "control.db"))
			case "symlink-dir":
				err = os.Symlink(base, filepath.Join(state, "run"))
			case "directory-file":
				err = os.Mkdir(filepath.Join(state, "daemon.lock"), 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if PrepareRuntime(state) == nil {
				t.Fatal("unsafe entry accepted")
			}
			info, _ := os.Stat(outside)
			data, _ := os.ReadFile(outside)
			if info.Mode().Perm() != 0644 || string(data) != "unchanged" {
				t.Fatal("outside object modified")
			}
		})
	}
}

// Privileged execution and pre-validation truncation fail before touching data.
func TestRuntimeIdentityAndTruncate(t *testing.T) {
	for _, pair := range [][2]int{{0, 100}, {100, 0}, {-1, 100}} {
		if RuntimeIdentity(pair[0], pair[1]) == nil {
			t.Fatal("privileged identity")
		}
	}
	if err := RuntimeIdentity(100, 100); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "data")
	os.WriteFile(p, []byte("keep"), 0600)
	if f, err := OpenPrivateFile(p, os.O_RDWR|os.O_TRUNC); err == nil {
		f.Close()
		t.Fatal("truncate accepted")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "keep" {
		t.Fatal("truncated")
	}
}
