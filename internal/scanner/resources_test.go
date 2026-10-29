package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// Each preset controls actual production ReadDir calls while yielding identical measured content.
func TestResourceModesActualBatchesAndContent(t *testing.T) {
	p, m := coverageFixture(t)
	for i := 0; i < 150; i++ {
		if err := os.WriteFile(filepath.Join(m.Roots[0].PhysicalPath, fmt.Sprintf("extra-%03d", i)), []byte("abc"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var baseline Result
	for _, mode := range []string{"gentle", "balanced", "fast"} {
		p.ResourceMode = mode
		policy, _ := resources.Resolve(mode, p.Limits)
		db, err := index.Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		ops := walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
			calls++
			if n != policy.DirectoryBatch {
				t.Fatalf("%s batch=%d", mode, n)
			}
			return f.ReadDir(n)
		}}
		result, err := scanManifestWithOperations(context.Background(), db, "resource-"+mode, p, m, index.DefaultFileTypeMapping(), ops)
		db.Close()
		if err != nil || calls == 0 {
			t.Fatal(result, err, calls)
		}
		if mode == "gentle" {
			baseline = result
		} else if result != baseline {
			t.Fatal(mode, result, baseline)
		}
		if result.Entries != 154 || result.Bytes != "458" || result.Errors != 0 {
			t.Fatal(result)
		}
	}
}

// Invalid policy is rejected before even a nil generation/source handle could be touched.
func TestResourcePolicyRejectedBeforeIO(t *testing.T) {
	for _, p := range []contracts.Profile{{}, {ResourceMode: "custom", Limits: contracts.ResourceLimits{Workers: 1}}, {ResourceMode: "fast", Limits: contracts.ResourceLimits{Workers: 2}}} {
		_, err := scanManifestWithOperations(context.Background(), nil, "invalid", p, scopemanifest.ScopeManifest{}, index.DefaultFileTypeMapping(), walkOperations{readDirectory: func(*os.File, int) ([]os.DirEntry, error) { t.Fatal("source touched"); return nil, nil }})
		if !errors.Is(err, resources.ErrUnsupported) {
			t.Fatal(err)
		}
	}
}

// Cancellation at an observed work boundary aborts the scan rather than recording a source gap.
func TestResourcePacingCancellation(t *testing.T) {
	p, m := coverageFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := index.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = scanManifestWithOperations(ctx, db, "cancel", p, m, index.DefaultFileTypeMapping(), walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
		entries, readErr := f.ReadDir(n)
		cancel()
		return entries, readErr
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
