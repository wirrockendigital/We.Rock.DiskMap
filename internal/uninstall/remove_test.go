package uninstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Fixtures contain only private app-owned files; source sentinels live outside the state root.
func fixture(t *testing.T) string {
	t.Helper()
	s := t.TempDir()
	if e := os.Chmod(s, 0700); e != nil {
		t.Fatal(e)
	}
	for _, d := range []string{"generations", "generations/11111111111111111111111111111111", "logs", "run"} {
		if e := os.Mkdir(filepath.Join(s, d), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, f := range []string{"daemon.lock", "control.db", "generations/11111111111111111111111111111111/index.db", "logs/slot-0.jsonl"} {
		if e := os.WriteFile(filepath.Join(s, f), []byte("fixture"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	return s
}

// Removal keeps the singleton inode and never affects outside source bytes.
func TestRemoveOwned(t *testing.T) {
	s := fixture(t)
	outside := filepath.Join(t.TempDir(), "source")
	os.WriteFile(outside, []byte("source"), 0600)
	prior, _ := os.Stat(filepath.Join(s, "daemon.lock"))
	if e := removeOwned(context.Background(), s, nil); e != nil {
		t.Fatal(e)
	}
	names, _ := os.ReadDir(s)
	if len(names) != 1 || names[0].Name() != "daemon.lock" {
		t.Fatal("unexpected remaining state")
	}
	now, _ := os.Stat(filepath.Join(s, "daemon.lock"))
	if !os.SameFile(prior, now) {
		t.Fatal("singleton replaced")
	}
	raw, _ := os.ReadFile(outside)
	if string(raw) != "source" {
		t.Fatal("source changed")
	}
	if CheckReady(s) != nil {
		t.Fatal("marker remained")
	}
}

// Every unsafe preflight leaves the control database intact and writes no removal marker.
func TestUnsafePreflightNoMutation(t *testing.T) {
	for _, kind := range []string{"unknown", "symlink", "hardlink", "permission", "cancelled", "locked"} {
		t.Run(kind, func(t *testing.T) {
			s := fixture(t)
			outside := filepath.Join(t.TempDir(), "sentinel")
			os.WriteFile(outside, []byte("preserve"), 0600)
			ctx := context.Background()
			switch kind {
			case "unknown":
				os.WriteFile(filepath.Join(s, "foreign"), nil, 0600)
			case "symlink":
				os.Symlink(outside, filepath.Join(s, "logs", "bad"))
			case "hardlink":
				os.Link(outside, filepath.Join(s, "logs", "bad"))
			case "permission":
				os.Chmod(filepath.Join(s, "control.db"), 0644)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "locked":
				f, e := os.OpenFile(filepath.Join(s, "daemon.lock"), os.O_RDWR, 0)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
					t.Fatal("lock failed")
				}
			}
			if removeOwned(ctx, s, nil) == nil {
				t.Fatal("unsafe accepted")
			}
			if _, e := os.Stat(filepath.Join(s, "control.db")); e != nil {
				t.Fatal("control deleted")
			}
			if _, e := os.Lstat(filepath.Join(s, "remove.pending")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("preflight marker written")
			}
			raw, _ := os.ReadFile(outside)
			if string(raw) != "preserve" {
				t.Fatal("sentinel changed")
			}
		})
	}
}

// Replaced directories never redirect unlink through a symlink; incomplete removal remains gated.
func TestExchangeAndResume(t *testing.T) {
	s := fixture(t)
	external := t.TempDir()
	os.WriteFile(filepath.Join(external, "index.db"), []byte("sentinel"), 0600)
	saved := filepath.Join(t.TempDir(), "saved")
	e := removeOwned(context.Background(), s, func() {
		if e := os.Rename(filepath.Join(s, "generations", "11111111111111111111111111111111"), saved); e != nil {
			t.Fatal(e)
		}
		os.Symlink(external, filepath.Join(s, "generations", "11111111111111111111111111111111"))
	})
	if e == nil || CheckReady(s) == nil {
		t.Fatal("exchange not blocked")
	}
	raw, _ := os.ReadFile(filepath.Join(external, "index.db"))
	if string(raw) != "sentinel" {
		t.Fatal("outside changed")
	}
	os.Remove(filepath.Join(s, "generations", "11111111111111111111111111111111"))
	os.Rename(saved, filepath.Join(s, "generations", "11111111111111111111111111111111"))
	if e = removeOwned(context.Background(), s, nil); e != nil {
		t.Fatal(e)
	}
	if CheckReady(s) != nil {
		t.Fatal("resume remained blocked")
	}
}

// Public entry cannot be used as a generic filesystem deletion API.
func TestRemoveRejectsNonPackagePath(t *testing.T) {
	s := fixture(t)
	if Remove(context.Background(), s) == nil {
		t.Fatal("arbitrary state accepted")
	}
	if _, e := os.Stat(filepath.Join(s, "control.db")); e != nil {
		t.Fatal("unexpected mutation")
	}
}

// A foreign owner is rejected independently of names, contents or directory placement.
type foreignInfo struct{ os.FileInfo }

func (i foreignInfo) Sys() any { v := *i.FileInfo.Sys().(*syscall.Stat_t); v.Uid++; return &v }
func TestForeignOwnerAndKnownNames(t *testing.T) {
	s := fixture(t)
	info, e := os.Stat(filepath.Join(s, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if owner(foreignInfo{info}) {
		t.Fatal("foreign inode accepted")
	}
	for _, tc := range []struct {
		p         string
		dir, want bool
	}{{"filter-cache", true, true}, {"filter-cache/" + strings.Repeat("a", 64) + ".owner", false, true}, {"filter-cache/" + strings.Repeat("a", 64) + ".db", false, true}, {"filter-cache/foreign.db", false, false}, {"run/source", false, false}, {"logs/source", false, false}, {"generations/11111111111111111111111111111111/index.db", false, true}, {"generations/11111111111111111111111111111111/private-source", false, false}, {"upgrades/11111111111111111111111111111111/manifest.json", false, true}} {
		if knownPath(tc.p, tc.dir) != tc.want {
			t.Fatalf("wrong path classification %s", tc.p)
		}
	}
}

// Special files are refused during the validation pass; no ordinary state is removed.
func TestFIFORejectedBeforeRemoval(t *testing.T) {
	s := fixture(t)
	if e := syscall.Mkfifo(filepath.Join(s, "logs", "slot-0.jsonl"), 0600); e == nil {
		t.Fatal("expected existing fixture")
	}
	os.Remove(filepath.Join(s, "logs", "slot-0.jsonl"))
	if e := syscall.Mkfifo(filepath.Join(s, "logs", "slot-0.jsonl"), 0600); e != nil {
		t.Fatal(e)
	}
	if removeOwned(context.Background(), s, nil) == nil {
		t.Fatal("FIFO accepted")
	}
	if _, e := os.Stat(filepath.Join(s, "control.db")); e != nil {
		t.Fatal("control removed")
	}
}

// Persistence failures cannot cross the destructive boundary, including retries with an existing marker.
func TestMarkerPersistenceFailurePreservesData(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, failure := range []string{"sync", "close"} {
			t.Run(fmt.Sprintf("%s-existing-%t", failure, existing), func(t *testing.T) {
				state := fixture(t)
				if existing {
					if e := os.WriteFile(filepath.Join(state, "remove.pending"), nil, 0600); e != nil {
						t.Fatal(e)
					}
				}
				before, e := os.ReadFile(filepath.Join(state, "control.db"))
				if e != nil {
					t.Fatal(e)
				}
				entered, closed := false, false
				syncMarker := func(fd int) error {
					if failure == "sync" {
						return syscall.EIO
					}
					return unix.Fsync(fd)
				}
				closeMarker := func(fd int) error {
					closed = true
					e := unix.Close(fd)
					if failure == "close" {
						return syscall.EIO
					}
					return e
				}
				e = removeOwnedWithMarkerIO(context.Background(), state, func() { entered = true }, syncMarker, closeMarker)
				if e == nil || entered || !closed {
					t.Fatal("persistence error crossed deletion boundary or leaked marker descriptor")
				}
				after, e := os.ReadFile(filepath.Join(state, "control.db"))
				if e != nil || !bytes.Equal(before, after) {
					t.Fatal("control state changed")
				}
				if CheckReady(state) == nil {
					t.Fatal("incomplete removal no longer blocks startup")
				}
				if e := removeOwned(context.Background(), state, nil); e != nil {
					t.Fatalf("retry failed: %v", e)
				}
			})
		}
	}
}
