package jobs

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// modeRows compares source semantics, excluding generation identity and capture timestamps.
func modeRows(t *testing.T, m *Manager, id string) map[string]string {
	t.Helper()
	db, e := index.Open(filepath.Join(m.state, "generations", id, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	out := map[string]string{}
	for _, record := range fixtureNodes(t, db) {
		n := record.Node
		size := "unknown"
		if n.LogicalBytes != nil {
			size = string(*n.LogicalBytes)
		}
		out[string(record.Path)] = n.Kind + ":" + size
	}
	return out
}

// TestModesTraverseDeepChanges proves reconciliation ignores parent-mtime shortcuts and preserves old bytes.
func TestModesTraverseDeepChanges(t *testing.T) {
	m, in, source := fixture(t)
	deep := filepath.Join(source, "a", "b")
	if e := os.MkdirAll(deep, 0700); e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string]string{"change": "before", "rename": "r", "move": "m", "delete": "gone"} {
		if e := os.WriteFile(filepath.Join(deep, name), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	run, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	baseline := terminal(t, m, run.Job.ID)
	if baseline.SnapshotState != "complete" {
		t.Fatal(baseline)
	}
	oldID := *baseline.Job.ResultSnapshotID
	// Hold the comparison snapshot like a real reader; retention may otherwise remove it after later scans.
	_, releaseBaseline, e := m.generationDB(oldID)
	if e != nil {
		t.Fatal(e)
	}
	defer releaseBaseline()
	oldPath := filepath.Join(m.state, "generations", oldID, "index.db")
	before, e := os.ReadFile(oldPath)
	if e != nil {
		t.Fatal(e)
	}
	oldRows := modeRows(t, m, oldID)
	times := map[string]time.Time{}
	for _, p := range []string{source, filepath.Join(source, "a"), deep} {
		info, e := os.Stat(p)
		if e != nil {
			t.Fatal(e)
		}
		times[p] = info.ModTime()
	}
	os.WriteFile(filepath.Join(deep, "change"), []byte("after is longer"), 0600)
	os.WriteFile(filepath.Join(deep, "added"), []byte("new"), 0600)
	os.Rename(filepath.Join(deep, "rename"), filepath.Join(deep, "renamed"))
	os.Rename(filepath.Join(deep, "move"), filepath.Join(source, "moved"))
	os.Remove(filepath.Join(deep, "delete"))
	for p, mt := range times {
		if e = os.Chtimes(p, mt, mt); e != nil {
			t.Fatal(e)
		}
		info, e := os.Stat(p)
		if e != nil || !info.ModTime().Equal(mt) {
			t.Fatal("mtime fixture", e)
		}
	}
	var expected map[string]string
	for _, mode := range []string{"reconcile", "rebuild", "full"} {
		run, e = m.Start(in.Profile.ID, mode)
		if e != nil {
			t.Fatal(e)
		}
		out := terminal(t, m, run.Job.ID)
		if out.SnapshotState != "complete" || out.Job.ScanMode != mode || out.CoverageErrors != 0 {
			t.Fatal(out)
		}
		got := modeRows(t, m, *out.Job.ResultSnapshotID)
		if got["a/b/change"] != "file:15" || got["a/b/added"] != "file:3" || got["a/b/renamed"] != "file:1" || got["moved"] != "file:1" {
			t.Fatal(got)
		}
		for _, deleted := range []string{"a/b/delete", "a/b/move", "a/b/rename"} {
			if _, ok := got[deleted]; ok {
				t.Fatal("stale entry", deleted)
			}
		}
		if expected == nil {
			expected = got
		} else if !reflect.DeepEqual(expected, got) {
			t.Fatal(expected, got)
		}
	}
	after, e := os.ReadFile(oldPath)
	if e != nil || sha256.Sum256(before) != sha256.Sum256(after) || !reflect.DeepEqual(oldRows, modeRows(t, m, oldID)) {
		t.Fatal("old generation changed", e)
	}
}

// TestRebuildWithoutOldIndex uses both missing and invalid previous index files without reading them.
func TestRebuildWithoutOldIndex(t *testing.T) {
	for _, damage := range []string{"missing", "invalid"} {
		t.Run(damage, func(t *testing.T) {
			m, in, _ := fixture(t)
			if e := m.ImportProfile(in); e != nil {
				t.Fatal(e)
			}
			first, e := m.Start(in.Profile.ID, "full")
			if e != nil {
				t.Fatal(e)
			}
			old := terminal(t, m, first.Job.ID)
			file := filepath.Join(m.state, "generations", *old.Job.ResultSnapshotID, "index.db")
			if damage == "missing" {
				e = os.Remove(file)
			} else {
				e = os.WriteFile(file, []byte("invalid old index"), 0600)
			}
			if e != nil {
				t.Fatal(e)
			}
			next, e := m.Start(in.Profile.ID, "rebuild")
			if e != nil {
				t.Fatal(e)
			}
			got := terminal(t, m, next.Job.ID)
			if got.SnapshotState != "complete" || got.Job.ScanMode != "rebuild" || got.Job.BytesObserved != "3" || got.Job.ResultSnapshotID == nil || *got.Job.ResultSnapshotID == *old.Job.ResultSnapshotID {
				t.Fatal(got)
			}
		})
	}
}

// TestModesNeverDeduplicateDifferentIntent isolates the active-job invariant without timing races.
func TestModesNeverDeduplicateDifferentIntent(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"full", "rebuild", "reconcile"} {
		m.active = &Outcome{Job: contracts.Job{ID: "existing", ProfileID: in.Profile.ID, ProfileRevision: in.Profile.Revision, ScanMode: mode, State: "scanning", BytesObserved: "0"}}
		got, e := m.Start(in.Profile.ID, mode)
		if e != nil || !got.Deduplicated || got.Job.ScanMode != mode {
			t.Fatal(got, e)
		}
		for _, other := range []string{"full", "rebuild", "reconcile"} {
			if other != mode {
				if _, e = m.Start(in.Profile.ID, other); !errors.Is(e, ErrConflict) {
					t.Fatal(other, e)
				}
			}
		}
	}
	m.active = nil
}

// TestModesReplayAndPartialKeepActive proves each new intent is durable and partial results stay inactive.
func TestModesReplayAndPartialKeepActive(t *testing.T) {
	m, in, source := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"full", "rebuild", "reconcile"} {
		key := BrowserStartIdentity{Principal: "admin", Key: "mode-" + mode}
		first, e := m.StartBrowser(in.Profile.ID, mode, key)
		if e != nil {
			t.Fatal(e)
		}
		done := terminal(t, m, first.Job.ID)
		again, e := m.StartBrowser(in.Profile.ID, mode, key)
		if e != nil || !again.Deduplicated || again.Job.ID != done.Job.ID || again.Job.ScanMode != mode {
			t.Fatal(again, e)
		}
		other := "rebuild"
		if mode == other {
			other = "full"
		}
		if _, e = m.StartBrowser(in.Profile.ID, other, key); !errors.Is(e, ErrIdempotencyConflict) {
			t.Fatal(e)
		}
		if e = os.Rename(source, source+"-moved"); e != nil {
			t.Fatal(e)
		}
		failed, e := m.Start(in.Profile.ID, mode)
		if e != nil {
			t.Fatal(e)
		}
		partial := terminal(t, m, failed.Job.ID)
		if e = os.Rename(source+"-moved", source); e != nil {
			t.Fatal(e)
		}
		var active string
		if e = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id=?`, in.Profile.ID).Scan(&active); e != nil || active != *done.Job.ResultSnapshotID || partial.SnapshotState != "partial" {
			t.Fatal(active, partial, e)
		}
	}
}
