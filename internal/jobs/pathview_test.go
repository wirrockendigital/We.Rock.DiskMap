package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// TestPathCopiesPreserveSnapshotProvenance uses real names and sealed generation metadata.
func TestPathCopiesPreserveSnapshotProvenance(t *testing.T) {
	m, in, source := fixture(t)
	names := []string{"Grüße 😀 % # \" ' <script>.txt", "literal%2Fname", string([]byte{'b', 0xff})}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(source, name), []byte("x"), 0600); err != nil {
			// APFS refuses invalid UTF-8; Linux native evidence remains a separate required probe.
			if runtime.GOOS == "darwin" && errors.Is(err, syscall.EILSEQ) {
				t.Log("host refuses invalid UTF-8 fixture; byte rendering is covered separately")
				continue
			}
			t.Fatal(err)
		}
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	view, err := m.Snapshot(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var selected string
	for _, node := range view.Nodes {
		out, err := m.Paths(ctx, "probe", *view.SnapshotID, node.ID, map[string]string{"Share": source})
		if err != nil {
			t.Fatal(err)
		}
		if node.OriginalName == names[0] {
			selected = node.ID
			if out.Name.Text != names[0] || out.NASPath.Text != filepath.Join(source, names[0]) || out.FileStationPath == nil || *out.FileStationPath != "/Share/"+names[0] {
				t.Fatal(out)
			}
		}
		if strings.Contains(node.OriginalName, `\xff`) {
			if out.Name.Encoding != "escaped-bytes" || out.NASPath.Encoding != "escaped-bytes" || out.FileStationPath != nil {
				t.Fatal(out)
			}
		}
	}
	if selected == "" {
		t.Fatal("fixture absent")
	}
	if err = os.Remove(filepath.Join(source, names[0])); err != nil {
		t.Fatal(err)
	}
	out, err := m.Paths(ctx, "probe", *view.SnapshotID, selected, map[string]string{"Share": source})
	if err != nil || out.FileStationPath != nil || out.NASPath.Text != filepath.Join(source, names[0]) {
		t.Fatal(out, err)
	}
	// Current mutable roots cannot rewrite the immutable NAS snapshot path.
	if _, err = m.db.Exec(`UPDATE profiles SET roots_json='{}' WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	out, err = m.Paths(ctx, "probe", *view.SnapshotID, selected, map[string]string{"Share": source})
	if err != nil || out.NASPath.Text != filepath.Join(source, names[0]) {
		t.Fatal(out, err)
	}
	if _, err = m.Paths(ctx, "probe", *view.SnapshotID, "unknown", nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE profiles SET revision=revision+1 WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Paths(ctx, "probe", *view.SnapshotID, selected, nil); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatal(err)
	}
}

// TestPathText never URI-decodes names and distinguishes escaped original bytes from ordinary backslashes.
func TestPathText(t *testing.T) {
	for _, value := range []string{"ä😀%2F#'\"<b>", strings.Repeat("segment/", 500) + "last", `literal\xff`} {
		got := pathText(value)
		if got.Text != value || got.Encoding != "utf8" {
			t.Fatal(got)
		}
	}
	got := pathText(string([]byte{'x', 0xff}))
	if got.Text != `"x\xff"` || got.Encoding != "escaped-bytes" {
		t.Fatal(got)
	}
}
