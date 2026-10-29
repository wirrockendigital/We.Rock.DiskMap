package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Malformed socket paths are rejected before creating directories or runtime files.
func TestRuntimeBoundaryBeforeSideEffects(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "absent", "wrong", "control.sock")
	if Run(context.Background(), path) == nil {
		t.Fatal("invalid path accepted")
	}
	if _, err := os.Stat(filepath.Join(base, "absent")); !os.IsNotExist(err) {
		t.Fatal("created state before validation")
	}
}
