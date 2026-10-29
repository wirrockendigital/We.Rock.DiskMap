package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// fixture creates only private sources, using canonical paths on macOS and Linux.
func fixture(t *testing.T) (*Manager, Import, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "data"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(context.Background(), filepath.Join(base, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	p := contracts.Profile{ID: "probe", Revision: 1, Name: "Probe", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share-one"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	return m, Import{SourceMode: "isolated_roots", RootKinds: map[string]string{"share-one": "share"}, Profile: p, Roots: map[string]string{"share-one": source}, VolumeIDs: map[string]string{"share-one": "volume-one"}}, source
}

// terminal waits for real persisted completion rather than using timer-derived progress.
func terminal(t *testing.T, m *Manager, id string) Outcome {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		o, err := m.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if terminalJob(o.Job.State) {
			return o
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Outcome{}
}

// TestGenerationPublication proves real metadata, source preservation and private modes.
func TestGenerationPublication(t *testing.T) {
	m, in, source := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	o := terminal(t, m, s.Job.ID)
	if o.Job.State != "completed" || o.Job.BytesObserved != "3" || o.Job.EntriesVisited != "2" || o.ServiceUID != os.Geteuid() {
		t.Fatalf("outcome: %+v", o)
	}
	var active string
	if err = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id='probe'`).Scan(&active); err != nil || active != *o.Job.ResultSnapshotID {
		t.Fatal(active, err)
	}
	dbpath := filepath.Join(m.state, "generations", active, "index.db")
	db, err := index.Open(dbpath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The production publisher must seal its actual scanner result before exposing the active pointer.
	header, err := index.ReadGenerationHeader(context.Background(), db, active)
	if err != nil || header.JobID != o.Job.ID || header.Profile.ID != in.Profile.ID || header.Profile.Revision != in.Profile.Revision || header.Entries != 2 || header.LogicalBytes != "3" || o.SnapshotEndedAt == nil || header.EndedAt != *o.SnapshotEndedAt || header.SchemaVersion != index.GenerationSchema || len(header.Manifest.Roots) != 1 {
		t.Fatal(header, err)
	}
	if _, err = db.Exec(`DELETE FROM nodes`); err == nil {
		t.Fatal("production generation was not sealed")
	}
	var node contracts.Node
	for _, record := range fixtureNodes(t, db) {
		if record.Node.ParentID != nil {
			node = record.Node
			break
		}
	}
	if node.VolumeID != "volume-one" || node.LogicalBytes == nil || *node.LogicalBytes != "3" {
		t.Fatal(node)
	}
	for _, path := range []string{filepath.Join(m.state, "control.db"), dbpath} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(path, info, err)
		}
	}
	body, err := os.ReadFile(filepath.Join(source, "data"))
	if err != nil || string(body) != "abc" {
		t.Fatal("source modified")
	}
	// A disappeared source produces a partial generation and preserves the complete pointer.
	if err = os.Rename(source, source+"-moved"); err != nil {
		t.Fatal(err)
	}
	next, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	partial := terminal(t, m, next.Job.ID)
	if partial.Job.State != "completed" || partial.SnapshotState != "partial" || partial.CoverageErrors != 1 {
		t.Fatal(partial)
	}
	var after string
	if err = m.db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id='probe'`).Scan(&after); err != nil || after != active {
		t.Fatal("partial replaced complete", after, err)
	}
}

// TestValidationAndSerialization tests unsupported settings and package-wide locking.
func TestValidationAndSerialization(t *testing.T) {
	m, in, _ := fixture(t)
	if other, err := Open(context.Background(), m.state); !errors.Is(err, ErrConflict) {
		if other != nil {
			other.Close()
		}
		t.Fatal("second daemon accepted", err)
	}
	bad := in
	bad.Profile.ResourceMode = "custom"
	if !errors.Is(m.ImportProfile(bad), ErrInvalid) {
		t.Fatal("unsupported policy accepted")
	}
	bad = in
	bad.Roots = map[string]string{"share-one": m.state}
	if !errors.Is(m.ImportProfile(bad), ErrInvalid) {
		t.Fatal("package state accepted")
	}
	bad = in
	bad.VolumeIDs = nil
	if !errors.Is(m.ImportProfile(bad), ErrInvalid) {
		t.Fatal("missing inventory accepted")
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.ImportProfile(in), ErrConflict) {
		t.Fatal("revision overwrite accepted")
	}
	if _, err := m.Start("missing", "full"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	// Pin an active record to exercise deterministic deduplication without timing assumptions.
	m.active = &Outcome{Job: contracts.Job{ID: "existing", ProfileID: "probe", ProfileRevision: 1, ScanMode: "full", BytesObserved: "0"}}
	joined, err := m.Start("probe", "full")
	if err != nil || !joined.Deduplicated || joined.Job.ID != "existing" {
		t.Fatal(joined, err)
	}
	if !errors.Is(m.ImportProfile(in), ErrConflict) {
		t.Fatal("active import accepted")
	}
	m.active = nil
}

// TestCancelledScanPersistsTerminal proves shutdown does not report a complete generation.
func TestCancelledScanPersistsTerminal(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	m.cancel()
	if _, err := m.Start("probe", "full"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

// TestRealFailureCancellationAndRecovery exercises persistent terminal state transitions.
func TestRealFailureCancellationAndRecovery(t *testing.T) {
	t.Run("storage failure", func(t *testing.T) {
		m, in, _ := fixture(t)
		if err := m.ImportProfile(in); err != nil {
			t.Fatal(err)
		}
		// Replace only the empty owned directory to inject a real publication failure.
		if err := os.Remove(filepath.Join(m.state, "generations")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(m.state, "generations"), []byte("block directory creation"), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := m.Start("probe", "full")
		if err != nil {
			t.Fatal(err)
		}
		o := terminal(t, m, s.Job.ID)
		if o.Job.State != "failed" || o.Job.ResultSnapshotID != nil {
			t.Fatal(o)
		}
	})
	t.Run("service interrupted traversal", func(t *testing.T) {
		m, in, _ := fixture(t)
		// Isolate direct execution from Open's empty queue admission before injecting cancellation.
		// The shared WaitGroup also owns persistent maintenance workers, so waiting on it would deadlock.
		deadline := time.Now().Add(5 * time.Second)
		for {
			m.mu.Lock()
			idle := !m.workerRunning
			closed := m.closed
			m.mu.Unlock()
			if closed {
				t.Fatal("manager closed before cancellation")
			}
			if idle {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("initial empty queue worker did not finish")
			}
			runtime.Gosched()
		}
		m.cancel()
		o := Outcome{ScopeManifest: &inventory.ScopeManifest{ContractVersion: inventory.ScopeManifestVersion, Roots: []inventory.ManifestRoot{{NormalizedRoot: inventory.NormalizedRoot{TargetID: "share-one", Kind: "share"}}}}, Job: contracts.Job{ID: "cancel-test", ProfileID: in.Profile.ID, CapturedProfile: in.Profile, State: "scanning", BytesObserved: "0"}}
		if err := m.save(o); err != nil {
			t.Fatal(err)
		}
		m.execute(o, profileSources{Version: 1, Mode: "isolated_roots"})
		got, err := m.Get(o.Job.ID)
		if err != nil || got.Job.State != "interrupted" || got.Job.ResultSnapshotID != nil {
			t.Fatal(got, err)
		}
	})
	t.Run("interrupted restart", func(t *testing.T) {
		m, in, _ := fixture(t)
		o := Outcome{Job: contracts.Job{ID: "interrupted-test", ProfileID: in.Profile.ID, State: "scanning", BytesObserved: "0"}}
		if err := m.save(o); err != nil {
			t.Fatal(err)
		}
		state := m.state
		if err := m.Close(); err != nil {
			t.Fatal(err)
		}
		restored, err := Open(context.Background(), state)
		if err != nil {
			t.Fatal(err)
		}
		defer restored.Close()
		got, err := restored.Get(o.Job.ID)
		if err != nil || got.Job.State != "interrupted" || got.Job.EndedAt == nil {
			t.Fatal(got, err)
		}
	})
}

// TestPermissionAndSymlinkCoverage verifies partial results and no traversal outside the root.
func TestPermissionAndSymlinkCoverage(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission denial requires an unprivileged test user")
	}
	m, in, source := fixture(t)
	denied := filepath.Join(source, "denied")
	os.Mkdir(denied, 0700)
	os.WriteFile(filepath.Join(denied, "hidden"), []byte("hidden"), 0600)
	os.Chmod(denied, 0000)
	defer os.Chmod(denied, 0700)
	if err := os.Symlink(filepath.Dir(source), filepath.Join(source, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	o := terminal(t, m, s.Job.ID)
	if o.Job.State != "completed" || o.SnapshotState != "partial" || o.CoverageErrors != 1 || o.Job.BytesObserved != "3" || o.Job.EntriesVisited != "4" {
		t.Fatal(o)
	}
}

// TestConcurrentRealScanJoinsOneJob proves deduplication while the real scanner is busy.
func TestConcurrentRealScanJoinsOneJob(t *testing.T) {
	m, in, source := fixture(t)
	for i := 0; i < 2000; i++ {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprint(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	other := in
	other.Profile.ID = "other"
	if err := m.ImportProfile(other); err != nil {
		t.Fatal(err)
	}
	first, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := m.Start("probe", "full")
	if err != nil || !joined.Deduplicated || joined.Job.ID != first.Job.ID {
		t.Fatal(joined, err)
	}
	if _, err = m.Start("other", "full"); err != nil {
		t.Fatal(err)
	}
	o := terminal(t, m, first.Job.ID)
	if o.Job.State != "completed" || o.Job.EntriesVisited != "2002" {
		t.Fatal(o)
	}
}

// TestImportedIdentityRejectsReplacement never indexes a replacement under the old volume mapping.
func TestImportedIdentityRejectsReplacement(t *testing.T) {
	for _, replacement := range []string{"directory", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			m, in, source := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			var raw string
			if err := m.db.QueryRow(`SELECT roots_json FROM profiles WHERE id=?`, in.Profile.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var stored map[string]scanner.RootBinding
			if err := json.Unmarshal([]byte(raw), &stored); err != nil {
				t.Fatal(err)
			}
			if stored["share-one"].Device == "" || stored["share-one"].Inode == "" {
				t.Fatal("identity not persisted", raw)
			}
			if err := os.Rename(source, source+"-original"); err != nil {
				t.Fatal(err)
			}
			if replacement == "directory" {
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "replacement"), []byte("must not scan"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(source+"-original", source); err != nil {
				t.Fatal(err)
			}
			submitted, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			out := terminal(t, m, submitted.Job.ID)
			if out.Job.State != "completed" || out.SnapshotState != "partial" || out.CoverageErrors != 1 || out.Job.EntriesVisited != "0" || out.Job.BytesObserved != "0" {
				t.Fatal("replacement was scanned", out)
			}
		})
	}
}
