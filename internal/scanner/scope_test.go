package scanner

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBoundPathRejectsTraversalMountMismatchAndLinks proves the shared reveal/source boundary.
func TestBoundPathRejectsTraversalMountMismatchAndLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(base, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("loop", filepath.Join(base, "loop")); err != nil {
		t.Fatal(err)
	}
	root, binding, err := OpenBoundRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	for _, relative := range []string{"../outside", "/etc/passwd", "a/../file", "loop", "loop/child", "file\x00ignored"} {
		if _, err = InspectBoundPath(binding, relative); err == nil {
			t.Fatal("accepted", relative)
		}
	}
	if _, err = InspectBoundPath(binding, "file"); err != nil {
		t.Fatal(err)
	}
	changed := binding
	changed.MountID = "other-mount"
	if _, err = InspectBoundPath(changed, "file"); err == nil {
		t.Fatal("mount mismatch accepted")
	}
	changed.MountID = ""
	if _, err = InspectBoundPath(changed, "file"); err == nil {
		t.Fatal("missing mount identity accepted")
	}
}

// TestLongDescriptorTraversal indexes paths beyond PATH_MAX without assembling a syscall pathname.
func TestLongDescriptorTraversal(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	relative := ""
	component := strings.Repeat("d", 180)
	for i := 0; i < 25; i++ {
		if err = unix.Mkdirat(fd, component, 0700); err != nil {
			unix.Close(fd)
			t.Fatal(err)
		}
		next, e := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		unix.Close(fd)
		if e != nil {
			t.Fatal(e)
		}
		fd = next
		if relative != "" {
			relative += "/"
		}
		relative += component
	}
	file, e := unix.Openat(fd, "leaf", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	unix.Close(fd)
	if e != nil {
		t.Fatal(e)
	}
	unix.Close(file)
	relative += "/leaf"
	root, binding, err := OpenBoundRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	if _, err = InspectBoundPath(binding, relative); err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profile := contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}}
	result, err := Scan(context.Background(), db, "long-snapshot", profile, map[string]RootBinding{"share": binding}, map[string]string{"share": "volume"})
	if err != nil || result.Errors != 0 || result.Entries != 27 {
		t.Fatal(result, err)
	}
	record, err := index.FindNode(context.Background(), db, "share", []byte(relative))
	if err != nil || string(record.Path) != relative {
		t.Fatal(len(record.Path), err)
	}
}

// TestRawPathDisplayDoesNotDefineIdentity preserves byte-distinct names behind safe JSON text.
func TestRawPathDisplayDoesNotDefineIdentity(t *testing.T) {
	raw := string([]byte{'n', 0xff})
	valid := "n�"
	if !utf8.ValidString(displayPath(raw)) || displayPath(raw) == displayPath(valid) {
		t.Fatal("byte identity lost")
	}
	encoded, err := json.Marshal(contracts.Node{OriginalName: displayPath(raw), RelativePath: displayPath(raw)})
	if err != nil || !utf8.Valid(encoded) {
		t.Fatal(err)
	}
	// The native writer preserves invalid Unix names even on hosts whose filesystem cannot create them.
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = index.InitializeGeneration(context.Background(), db, "raw"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	w, err := index.NewNodeWriter(context.Background(), tx, "raw")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	rootID, err := w.AddRoot("share", "volume", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := w.Insert(rootID, 0, []byte("."), "directory", nil, nil, "1:2:3:4", nil)
	if err != nil {
		t.Fatal(err)
	}
	size := contracts.Bytes("1")
	for _, name := range []string{raw, valid} {
		if _, err = w.Insert(rootID, parent, []byte(name), "file", &size, &size, "1:3:3:4", nil); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	a, err := index.FindNode(context.Background(), db, "share", []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := index.FindNode(context.Background(), db, "share", []byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if a.Node.ID == b.Node.ID || string(a.Name) != raw || string(b.Name) != valid || string(a.Path) != raw || string(b.Path) != valid {
		t.Fatal("raw numeric identity lost")
	}

}

// TestDepthBudgetReportsIncompleteCoverage avoids unbounded descriptor/stack consumption.
func TestDepthBudgetReportsIncompleteCoverage(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 260; i++ {
		if err = unix.Mkdirat(fd, "d", 0700); err != nil {
			unix.Close(fd)
			t.Fatal(err)
		}
		next, e := unix.Openat(fd, "d", unix.O_RDONLY|unix.O_DIRECTORY, 0)
		unix.Close(fd)
		if e != nil {
			t.Fatal(e)
		}
		fd = next
	}
	unix.Close(fd)
	root, binding, err := OpenBoundRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	profile := contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}}
	result, err := Scan(context.Background(), db, "deep-snapshot", profile, map[string]RootBinding{"share": binding}, map[string]string{"share": "volume"})
	if err != nil || result.Errors != 1 || result.Entries != 257 {
		t.Fatal(result, err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM coverage WHERE target_id='share'`).Scan(&state); err != nil || state != "partial" {
		t.Fatal(state, err)
	}
}
