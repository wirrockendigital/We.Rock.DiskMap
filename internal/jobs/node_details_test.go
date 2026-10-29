package jobs

import (
	"context"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Real publication and an offline source prove that detail requests do not perform live source reads.
func TestNodeDetailsHistoricalBinding(t *testing.T) {
	m, in, source := fixture(t)
	name := "Grüße <saved>.txt"
	if e := os.WriteFile(filepath.Join(source, name), []byte("historical"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	started, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, started.Job.ID)
	view, e := m.OpenView(context.Background(), "admin", in.Profile.ID, "detail-view", "", false)
	if e != nil {
		t.Fatal(e)
	}
	snap, e := m.Snapshot(context.Background(), in.Profile.ID)
	if e != nil {
		t.Fatal(e)
	}
	var node string
	for _, n := range snap.Nodes {
		if n.OriginalName == name {
			node = n.ID
		}
	}
	if node == "" {
		t.Fatal("fixture")
	}
	req := NodeDetailsRequest{ContractVersion: 1, ProfileID: in.Profile.ID, ViewID: view.Lease.ID, SnapshotID: *snap.SnapshotID, RootID: "nas", NodeID: node, Metric: "logical", Operation: "details"}
	if e = os.Rename(source, source+"-offline"); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(source+"-offline", source)
	m.db.Exec(`UPDATE profiles SET revision=revision+1 WHERE id=?`, in.Profile.ID)
	out, e := m.ReadNodeDetails(context.Background(), "admin", req)
	if e != nil || out.Paths == nil || out.Paths.NASPath.Text != filepath.Join(source, name) || *out.Detail.Node.LogicalBytes != "10" || out.Detail.ProfileRevision != 1 {
		t.Fatal(out, e)
	}
	if e = m.cleanupGeneration(context.Background(), in.Profile.ID, req.SnapshotID, done.Job.ID, ""); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = m.ReadNodeDetails(context.Background(), "other", req); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	wrong := req
	wrong.SnapshotID = "wrong"
	if _, e = m.ReadNodeDetails(context.Background(), "admin", wrong); !errors.Is(e, query.ErrSnapshot) {
		t.Fatal(e)
	}
	wrong = req
	wrong.RootID = "unknown"
	if _, e = m.ReadNodeDetails(context.Background(), "admin", wrong); !errors.Is(e, query.ErrRoot) {
		t.Fatal(e)
	}
	m.readerMu.Lock()
	m.viewLeases[req.ViewID].Expires = time.Now().Add(-time.Minute)
	m.readerMu.Unlock()
	if _, e = m.ReadNodeDetails(context.Background(), "admin", req); !errors.Is(e, ErrViewExpired) {
		t.Fatal(e)
	}
}
