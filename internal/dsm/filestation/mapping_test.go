package filestation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// TestIdentityMapping exercises real directory metadata, renamed registry roots and removal recovery.
func TestIdentityMapping(t *testing.T) {
	base := realTemp(t)
	source := filepath.Join(base, "original")
	if err := os.MkdirAll(filepath.Join(source, "deep", "leaf"), 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(source, "deep", "leaf", "data")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, binding, err := scanner.OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	identities := map[string]string{}
	for _, rel := range []string{".", "deep", "deep/leaf", "deep/leaf/data"} {
		info, err := os.Lstat(filepath.Join(source, rel))
		if err != nil {
			t.Fatal(err)
		}
		identities[rel] = scanner.ObjectIdentity(info)
	}
	lookup := func(rel string) (string, error) {
		value, ok := identities[rel]
		if !ok {
			return "", errors.New("unindexed")
		}
		return value, nil
	}
	resolve := func(shares map[string]string) (Target, error) {
		return Resolve(context.Background(), binding, "deep/leaf/data", "file", identities["deep/leaf/data"], shares, lookup)
	}
	out, err := resolve(map[string]string{"Share": source})
	if err != nil || out.FileStationPath != "/Share/deep/leaf/data" || out.ParentPath != "/Share/deep/leaf" || out.NoticeCode != "" {
		t.Fatal(out, err)
	}
	renamed := filepath.Join(base, "renamed")
	if err = os.Rename(source, renamed); err != nil {
		t.Fatal(err)
	}
	out, err = resolve(map[string]string{"Current share": renamed})
	if err != nil || out.FileStationPath != "/Current share/deep/leaf/data" {
		t.Fatal(out, err)
	}
	// A same-name replacement must not inherit the old indexed object's authority.
	file = filepath.Join(renamed, "deep", "leaf", "data")
	if err = os.Rename(file, file+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = resolve(map[string]string{"Current share": renamed}); err == nil {
		t.Fatal("accepted replacement")
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	out, err = resolve(map[string]string{"Current share": renamed})
	if err != nil || out.Kind != "directory" || out.FileStationPath != "/Current share/deep/leaf" || out.NoticeCode != "FILE_REMOVED_PARENT_AVAILABLE" {
		t.Fatal(out, err)
	}
	// Hold the removed inode so Linux cannot recycle it for the replacement fixture.
	// Directory identity intentionally survives child-induced ctime changes.
	removedParent, err := os.Open(filepath.Join(renamed, "deep", "leaf"))
	if err != nil {
		t.Fatal(err)
	}
	defer removedParent.Close()
	// Removing more ancestors can recover only an indexed, surviving directory.
	if err = os.RemoveAll(filepath.Join(renamed, "deep", "leaf")); err != nil {
		t.Fatal(err)
	}
	out, err = resolve(map[string]string{"Current share": renamed})
	if err != nil || out.FileStationPath != "/Current share/deep" {
		t.Fatal(out, err)
	}
	if err = os.Mkdir(filepath.Join(renamed, "deep", "leaf"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = resolve(map[string]string{"Current share": renamed}); err == nil {
		t.Fatal("accepted replaced parent")
	}
}

// TestMappingRejectsUnverifiedTargets proves that names and lexical resemblance are insufficient.
func TestMappingRejectsUnverifiedTargets(t *testing.T) {
	base := realTemp(t)
	source := filepath.Join(base, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	root, binding, err := scanner.OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	info, err := os.Lstat(filepath.Join(source, "data"))
	if err != nil {
		t.Fatal(err)
	}
	id := scanner.ObjectIdentity(info)
	for name, shares := range map[string]map[string]string{"unmapped": {}, "similar-prefix": {"wrong": source + "-other"}, "virtual": {"wrong": "virtual://root"}, "system": {"@system": source}, "ambiguous": {"a": source, "b": source}, "invalidname": {"a/b": source}} {
		t.Run(name, func(t *testing.T) {
			if out, err := Resolve(context.Background(), binding, "data", "file", id, shares, nil); err == nil {
				t.Fatal(out)
			}
		})
	}
	// A symlink at the final component is not a deleted-file recovery candidate.
	if err = os.Remove(filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
	if _, err = Resolve(context.Background(), binding, "data", "file", id, map[string]string{"share": source}, func(string) (string, error) { return id, nil }); err == nil {
		t.Fatal("accepted link")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Resolve(ctx, binding, "data", "file", id, map[string]string{"share": source}, nil); err == nil {
		t.Fatal("ignored cancellation")
	}
}

// TestVolumeMapping uses actual registry descendants and preserves distinct share identities.
func TestVolumeMapping(t *testing.T) {
	volume := realTemp(t)
	for _, name := range []string{"one", "two"} {
		if err := os.Mkdir(filepath.Join(volume, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	root, binding, err := scanner.OpenBoundRoot(volume)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	info, err := os.Stat(filepath.Join(volume, "two"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Resolve(context.Background(), binding, "two", "directory", scanner.ObjectIdentity(info), map[string]string{"First": filepath.Join(volume, "one"), "Second": filepath.Join(volume, "two")}, nil)
	if err != nil || out.FileStationPath != "/Second" {
		t.Fatal(out, err)
	}
	// The volume itself has no logical File Station share target.
	info, err = os.Stat(volume)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Resolve(context.Background(), binding, ".", "directory", scanner.ObjectIdentity(info), map[string]string{"Second": filepath.Join(volume, "two")}, nil); err == nil {
		t.Fatal("mapped volume root")
	}
}

// realTemp removes the host's /var alias before testing deliberately no-follow source handling.
func realTemp(t *testing.T) string {
	t.Helper()
	value, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return value
}
