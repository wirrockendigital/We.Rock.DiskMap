package inventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Real descriptor probes distinguish empty bins, missing bins, links and cancelled observations.
func TestRecycleObservation(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if got := ObserveRecycle(ctx, base); got != "missing" {
		t.Fatal(got)
	}
	bin := filepath.Join(base, "#recycle")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if got := ObserveRecycle(ctx, base); got != "readable" {
		t.Fatal(got)
	}
	if err = os.WriteFile(filepath.Join(bin, "old.txt"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := ObserveRecycle(ctx, base); got != "readable" {
		t.Fatal(got)
	}
	if os.Geteuid() != 0 {
		if err = os.Chmod(bin, 0000); err != nil {
			t.Fatal(err)
		}
		if got := ObserveRecycle(ctx, base); got != "denied" {
			t.Fatal(got)
		}
		if err = os.Chmod(bin, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Rename(bin, filepath.Join(base, "saved")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("saved", bin); err != nil {
		t.Fatal(err)
	}
	if got := ObserveRecycle(ctx, base); got != "unknown" {
		t.Fatal(got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if got := ObserveRecycle(cancelled, base); got != "unknown" {
		t.Fatal(got)
	}
	if got := ObserveRecycle(ctx, filepath.Join(base, "absent-share")); got != "unknown" {
		t.Fatal(got)
	}
}
