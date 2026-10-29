package jobs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestPackageUsageMeasuresOnlyOwnRegularFiles verifies no-follow behavior and explicit budgets.
func TestPackageUsageMeasuresOnlyOwnRegularFiles(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, outside := filepath.Join(base, "state"), filepath.Join(base, "outside")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(state, "index.db"), []byte("own"), 0000)
	os.WriteFile(filepath.Join(outside, "source"), []byte("never count this source"), 0600)
	os.Symlink(outside, filepath.Join(state, "link"))
	if err := syscall.Mkfifo(filepath.Join(state, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	out := measurePackageUsage(context.Background(), state, 100, time.Second)
	if out.State != "complete" || out.LogicalBytes == nil || *out.LogicalBytes != "3" || out.AllocatedBytes == nil || out.Method != "package-state-stat" {
		t.Fatal(out)
	}
	if _, err := time.Parse(time.RFC3339Nano, out.MeasuredAt); err != nil {
		t.Fatal(err)
	}
	for _, result := range []struct {
		limit  int
		budget time.Duration
	}{{1, time.Second}, {100, 0}} {
		got := measurePackageUsage(context.Background(), state, result.limit, result.budget)
		if got.State != "partial" || got.LogicalBytes != nil || got.AllocatedBytes != nil {
			t.Fatal(got)
		}
	}
	if got := packageUsage(context.Background(), filepath.Join(base, "missing")); got.State != "unavailable" || got.LogicalBytes != nil {
		t.Fatal(got)
	}
}
