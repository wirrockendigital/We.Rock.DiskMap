package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUUIDMetadataBoundaries rejects ambiguous or injected identity metadata.
func TestUUIDMetadataBoundaries(t *testing.T) {
	uuid := "11111111-1111-1111-1111-111111111111"
	raw := `{"uuid":"` + uuid + `"}`
	if got, err := ParseShareUUID(strings.NewReader(raw)); err != nil || got != uuid {
		t.Fatal(got, err)
	}
	for _, bad := range []string{raw + raw, `{"uuid":"` + uuid + `","uuid":"` + uuid + `"}`, `{"uuid":"` + uuid + `","extra":true}`, `{"uuid":"name"}`, strings.Repeat("x", 4097)} {
		if _, err := ParseShareUUID(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted invalid UUID metadata")
		}
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "@eaDir", "share")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "SYNO@.attr"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadShareUUID(base, "share"); err != nil || got != uuid {
		t.Fatal(got, err)
	}
	if err = os.Rename(filepath.Join(dir, "SYNO@.attr"), filepath.Join(dir, "real")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink("real", filepath.Join(dir, "SYNO@.attr")); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadShareUUID(base, "share"); err == nil {
		t.Fatal("followed metadata symlink")
	}
}

// TestRightsMetadata qualifies only explicit observed encryption flags and complete path records.
func TestRightsMetadata(t *testing.T) {
	input := "[plain]\npath=/volume1/plain\n[locked]\n\tpath=/volume2/locked\n\tencryption=yes\n\tlenEncKey=32\n"
	rights, err := ParseShareRights(strings.NewReader(input))
	if err != nil || rights["plain"].Encrypted || !rights["locked"].Encrypted {
		t.Fatal(rights, err)
	}
	locked, err := ParseShareRights(strings.NewReader("[locked]\npath=/volume1/locked\nencryption=show\n"))
	if err != nil || !locked["locked"].Locked || !locked["locked"].Encrypted {
		t.Fatal(locked, err)
	}

	for _, bad := range []string{"[x]\npath=/volume1/x\npath=/volume2/x", "[x]\npath=/volume1/x\nencryption=no", "[x]\npath=/volume1/x\n[X]\npath=/volume2/x", "[x]\nlenEncKey=32", "[x]\npath=/volume1/x\ninclude=elsewhere"} {
		if _, err := ParseShareRights(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted invalid rights metadata")
		}
	}
}
