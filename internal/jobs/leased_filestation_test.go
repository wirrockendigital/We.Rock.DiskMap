package jobs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLeasedFileStationHistorical proves a newer publication never substitutes the selected old node.
func TestLeasedFileStationHistorical(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	name := "Grüße % #.txt"
	if e := os.WriteFile(filepath.Join(source, name), []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	run, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, run.Job.ID)
	view, e := m.OpenView(ctx, "admin", in.Profile.ID, "historical-fs", "", false)
	if e != nil {
		t.Fatal(e)
	}
	var id string
	for _, n := range view.View.Nodes {
		if n.OriginalName == name {
			id = n.ID
		}
	}
	if id == "" {
		t.Fatal("missing fixture node")
	}
	request := LeasedPathsRequest{1, in.Profile.ID, view.Lease.ID, view.Lease.SnapshotID, id}
	run, e = m.Start(in.Profile.ID, "rebuild")
	if e != nil {
		t.Fatal(e)
	}
	terminal(t, m, run.Job.ID)
	shares := map[string]string{"Share": source}
	out, e := m.ReadLeasedFileStation(ctx, "admin", request, shares)
	if e != nil || out.FileStationPath != "/Share/"+name || out.Kind != "file" {
		t.Fatal(out, e)
	}
	// A held writer connection must not block the leased observation pool.
	held, err := m.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	_, err = m.ReadLeasedFileStation(bounded, "admin", request, shares)
	cancel()
	held.Close()
	if err != nil {
		t.Fatalf("held writer: %v", err)
	}
	// A WAL writer cannot block the committed leased observation.
	exclusive, err := m.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exclusive.ExecContext(ctx, "BEGIN EXCLUSIVE"); err != nil {
		exclusive.Close()
		t.Fatal(err)
	}
	blocked, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = m.ReadLeasedFileStation(blocked, "admin", request, shares)
	stop()
	exclusive.ExecContext(ctx, "ROLLBACK")
	exclusive.Close()
	if err != nil {
		t.Fatalf("WAL writer blocked observation: %v", err)
	}
	if _, e = m.ReadLeasedFileStation(ctx, "foreign", request, shares); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	wrong := request
	wrong.SnapshotID = "wrong"
	if _, e = m.ReadLeasedFileStation(ctx, "admin", wrong, shares); e == nil {
		t.Fatal("wrong snapshot accepted")
	}
	wrong = request
	wrong.NodeID = "nas"
	if _, e = m.ReadLeasedFileStation(ctx, "admin", wrong, shares); e == nil {
		t.Fatal("virtual root accepted")
	}
	// Missing files may offer only the verified original parent; a same-name replacement never does.
	if e = os.Rename(filepath.Join(source, name), filepath.Join(source, name+"-moved")); e != nil {
		t.Fatal(e)
	}
	out, e = m.ReadLeasedFileStation(ctx, "admin", request, shares)
	if e != nil || out.NoticeCode != "FILE_REMOVED_PARENT_AVAILABLE" || out.FileStationPath != "/Share" {
		t.Fatal(out, e)
	}
	if e = os.WriteFile(filepath.Join(source, name), []byte("replacement"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ReadLeasedFileStation(ctx, "admin", request, shares); !errors.Is(e, ErrFileStationUnavailable) {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(source, name)); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(source, name+"-moved"), filepath.Join(source, name)); e != nil {
		t.Fatal(e)
	}
	if _, e = m.db.Exec(`UPDATE profiles SET revision=revision+1 WHERE id=?`, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ReadLeasedFileStation(ctx, "admin", request, shares); !errors.Is(e, ErrStaleSnapshot) {
		t.Fatal(e)
	}
	if _, e = m.db.Exec(`UPDATE profiles SET revision=revision-1 WHERE id=?`, in.Profile.ID); e != nil {
		t.Fatal(e)
	}
	m.viewClock = func() time.Time { return time.Now().Add(viewTTL + time.Minute) }
	if _, e = m.ReadLeasedFileStation(ctx, "admin", request, shares); !errors.Is(e, ErrViewExpired) {
		t.Fatal(e)
	}
}
