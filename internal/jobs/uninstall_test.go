package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An interrupted removal must not let normal startup recreate a control database.
func TestUninstallMarkerBlocksControlCreation(t *testing.T) {
	state := t.TempDir()
	os.Chmod(state, 0700)
	if e := os.WriteFile(filepath.Join(state, "remove.pending"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	m, e := Open(context.Background(), state)
	if e == nil {
		m.Close()
		t.Fatal("partial removal accepted")
	}
	if _, e := os.Stat(filepath.Join(state, "control.db")); !os.IsNotExist(e) {
		t.Fatal("control recreated")
	}
}
