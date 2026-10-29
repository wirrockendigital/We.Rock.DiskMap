package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViews use actual SQLite generations and source metadata, not invented snapshots.
func TestViews(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	empty, err := m.Snapshot(ctx, "probe")
	if err != nil || empty.SnapshotID != nil || len(empty.Nodes) != 0 {
		t.Fatal(empty, err)
	}
	if _, err = m.Snapshot(ctx, "x' OR 1=1--"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	run, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	profiles, err := m.Profiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].ActiveSnapshotID == nil {
		t.Fatal(profiles, err)
	}
	view, err := m.Snapshot(ctx, "probe")
	if err != nil || len(view.Nodes) != 2 || view.Truncated {
		t.Fatal(view, err)
	}
	var fileID string
	for _, n := range view.Nodes {
		if n.Kind == "file" {
			fileID = n.ID
		}
	}
	target, err := m.FileStation(ctx, "probe", *view.SnapshotID, fileID, map[string]string{"share": source})
	if err != nil || target.FileStationPath != "/share/data" || target.ParentPath != "/share" {
		t.Fatal(target, err)
	}
	if _, err = m.FileStation(ctx, "probe", "stale", fileID, map[string]string{"share": source}); !errors.Is(err, ErrStaleSnapshot) {
		t.Fatal(err)
	}
	if _, err = m.FileStation(ctx, "probe", *view.SnapshotID, fileID, map[string]string{}); !errors.Is(err, ErrFileStationUnavailable) {
		t.Fatal(err)
	}
	// An unknown or malicious ID cannot reveal whether any mapped target exists.
	for _, unknown := range []string{"unknown-node", "../private", "n' OR 1=1--"} {
		if _, err = m.FileStation(ctx, "probe", *view.SnapshotID, unknown, map[string]string{"share": source}); !errors.Is(err, ErrNotFound) {
			t.Fatal(unknown, err)
		}
	}
	if err = os.Remove(filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
	if recovered, err := m.FileStation(ctx, "probe", *view.SnapshotID, fileID, map[string]string{"share": source}); err != nil || recovered.FileStationPath != "/share" || recovered.NoticeCode != "FILE_REMOVED_PARENT_AVAILABLE" || recovered.Kind != "directory" {
		t.Fatal(recovered, err)
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(source, "data")); err != nil {
		t.Fatal(err)
	}
	if _, err = m.FileStation(ctx, "probe", *view.SnapshotID, fileID, map[string]string{"share": source}); !errors.Is(err, ErrFileStationUnavailable) {
		t.Fatal(err)
	}
}

// TestBoundedViews proves truncation on genuine records without loading a complete index.
func TestBoundedViews(t *testing.T) {
	m, in, source := fixture(t)
	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprint(i)), nil, 0600); err != nil {
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
	if err != nil || !view.Truncated || len(view.Nodes) > 200 || len(view.Nodes) == 0 {
		t.Fatal(len(view.Nodes), view.Truncated, err)
	}
}

// TestShareParser rejects ambiguous mappings and Samba directives that change their meaning.
func TestShareParser(t *testing.T) {
	shares, err := ParseShares(strings.NewReader("[share]\n\tpath = /volume1/share\n read only=yes\n[other]\n path=/volume2/other\n"))
	if err != nil || shares["share"] != "/volume1/share" {
		t.Fatal(shares, err)
	}
	for _, raw := range []string{"[share]\npath=/volume1/a\npath=/volume1/b", "[share]\npath=/volume1/a\n[SHARE]\npath=/volume1/b", "[share]\ninclude=/etc/other", "[share]\npath=relative", "[share]\npath=/volume1/a\n[other]\npath=/volume1/a", "[share]\npath=/volume1/%U", "[share]\npath=/volume1/a\ncopy=other"} {
		if _, err := ParseShares(strings.NewReader(raw)); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

// TestProfileRevisionStopsOldTargetMapping prevents remapping retained snapshots to a new scope.
func TestProfileRevisionStopsOldTargetMapping(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	empty, err := m.Profiles(context.Background())
	if err != nil || empty[0].LatestJobID != nil {
		t.Fatal(empty, err)
	}
	run, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	profiles, err := m.Profiles(context.Background())
	if err != nil || profiles[0].LatestJobID == nil || *profiles[0].LatestJobID != run.Job.ID {
		t.Fatal(profiles, err)
	}
	view, err := m.Snapshot(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	in.Profile.Revision++
	if err = m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	_, err = m.FileStation(context.Background(), "probe", *view.SnapshotID, view.Nodes[0].ID, map[string]string{"share": source})
	if !errors.Is(err, ErrStaleSnapshot) {
		t.Fatal(err)
	}
}

// TestRevealScopeAndEncodingErrors cannot turn stale or ambiguous bytes into a different source path.
func TestRevealScopeAndEncodingErrors(t *testing.T) {
	m, in, source := fixture(t)
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
	var node contracts.Node
	for _, n := range view.Nodes {
		if n.Kind == "file" {
			node = n
		}
	}
	db, err := index.Open(filepath.Join(m.state, "generations", *view.SnapshotID, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Bypass the write seal explicitly to test independent action-time corruption checks.
	if _, err = db.Exec(`DROP TRIGGER seal_nodes_UPDATE; DROP TRIGGER seal_roots_UPDATE`); err != nil {
		t.Fatal(err)
	}
	call := func(id string) error {
		_, err := m.FileStation(context.Background(), "probe", *view.SnapshotID, id, map[string]string{"share": source})
		return err
	}
	for _, kind := range []string{"virtual", "file"} {
		changed := node
		changed.Kind = kind
		if kind == "file" {
			changed.VolumeID = "foreign-volume"
		}
		if _, err = db.Exec(`UPDATE nodes SET kind=? WHERE id=?`, changed.Kind, node.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`UPDATE roots SET volume_id=? WHERE id=(SELECT root_id FROM nodes WHERE id=?)`, changed.VolumeID, node.ID); err != nil {
			t.Fatal(err)
		}
		// The compact reader rejects an invalid physical kind before action-time scope mapping.
		want := ErrNotFound
		if kind == "virtual" {
			want = index.ErrGeneration
		}
		if e := call(node.ID); !errors.Is(e, want) {
			t.Fatal("foreign node outcome", kind, e)
		}
	}
	if _, err = db.Exec(`UPDATE nodes SET kind=? WHERE id=?`, node.Kind, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE roots SET volume_id=? WHERE id=(SELECT root_id FROM nodes WHERE id=?)`, node.VolumeID, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`PRAGMA user_version=0`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(call(node.ID), ErrGenerationRefresh) {
		t.Fatal("old generation reveal accepted")
	}
	db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, index.GenerationSchema))
	var roots string
	if err = m.db.QueryRow(`SELECT roots_json FROM profiles WHERE id='probe'`).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE profiles SET roots_json=json_remove(roots_json,'$."share-one".mountId') WHERE id='probe'`); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(call(node.ID), ErrRootRebind) {
		t.Fatal("missing mount ID accepted")
	}
	m.db.Exec(`UPDATE profiles SET roots_json=? WHERE id='probe'`, roots)
	rawPath := []byte{'b', 0xff}
	changed := node
	// A numeric locator stays fixed; corruption changes its original basename rather than a path hash.
	changed.RelativePath = `"b\\xff"`
	changed.OriginalName = changed.RelativePath
	if _, err = db.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, rawPath, node.ID); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(call(changed.ID), ErrPathEncoding) {
		t.Fatal("invalid original bytes reached source lookup")
	}
}

// TestFileStationStoredIdentity rejects replacement and uses a renamed live share mapping.
func TestFileStationStoredIdentity(t *testing.T) {
	m, in, source := fixture(t)
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
	fileID := ""
	for _, n := range view.Nodes {
		if n.Kind == "file" {
			fileID = n.ID
		}
	}
	renamed := source + "-renamed"
	if err = os.Rename(source, renamed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(renamed, source) })
	resolve := func() (FileStationTarget, error) {
		return m.FileStation(context.Background(), "probe", *view.SnapshotID, fileID, map[string]string{"Renamed": renamed})
	}
	out, err := resolve()
	if err != nil || out.FileStationPath != "/Renamed/data" {
		t.Fatal(out, err)
	}
	if err = os.Rename(filepath.Join(renamed, "data"), filepath.Join(renamed, "previous")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(renamed, "data"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = resolve(); !errors.Is(err, ErrFileStationUnavailable) {
		t.Fatal("replacement exposed", err)
	}
}
