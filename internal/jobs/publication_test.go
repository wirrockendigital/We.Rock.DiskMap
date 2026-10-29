package jobs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const testPublicationTimeout = 25 * time.Second

// publicationPhases enumerate each durable filesystem and transactional boundary.
var publicationPhases = []string{"phase_preflight", "phase_scanning", "phase_validating", "phase_publishing", "before_staged_sync", "after_staged_sync", "before_rename", "after_rename", "before_parent_sync", "after_parent_sync", "before_transaction", "before_job_write", "after_job_write", "before_pointer_write", "after_pointer_write", "before_commit"}

// TestPublicationFailuresPreserveLastGood injects disk-full errors without consuming host storage.
func TestPublicationFailuresPreserveLastGood(t *testing.T) {
	for _, phase := range publicationPhases {
		t.Run(phase, func(t *testing.T) {
			m, in, _ := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			first, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			good := terminal(t, m, first.Job.ID)
			file := filepath.Join(m.state, "generations", *good.Job.ResultSnapshotID, "index.db")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			m.publishHook = func(p string) error {
				if p == phase {
					return syscall.ENOSPC
				}
				return nil
			}
			m.phaseHook = func(p string) error { return m.publishHook("phase_" + p) }
			next, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			failed := terminal(t, m, next.Job.ID)
			if failed.Job.State != "failed" || failed.Job.ResultSnapshotID != nil {
				t.Fatalf("unexpected failed publication: %+v", failed)
			}
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("last good bytes changed", err)
			}
			view, err := m.Snapshot(context.Background(), in.Profile.ID)
			if err != nil || view.SnapshotID == nil || *view.SnapshotID != *good.Job.ResultSnapshotID {
				t.Fatal("last good pointer changed", err)
			}
		})
	}
}

// TestPublicationCrashChild runs only in the dedicated test subprocess, never in the daemon.
func TestPublicationCrashChild(t *testing.T) {
	state := os.Getenv("DISKMAP_PUBLICATION_TEST_STATE")
	if state == "" {
		t.Skip("subprocess helper")
	}
	m, err := Open(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	phase := os.Getenv("DISKMAP_PUBLICATION_TEST_PHASE")
	m.publishHook = func(p string) error {
		if p == phase {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		}
		return nil
	}
	m.phaseHook = func(p string) error { return m.publishHook("phase_" + p) }
	job, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, job.Job.ID)
	t.Fatal("kill boundary not reached")
}

// TestPublicationProcessKills proves restart state on both sides of the actual SQLite commit.
func TestPublicationProcessKills(t *testing.T) {
	for _, phase := range append(append([]string{}, publicationPhases...), "after_commit") {
		t.Run(phase, func(t *testing.T) {
			m, in, _ := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			first, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			good := terminal(t, m, first.Job.ID)
			old := *good.Job.ResultSnapshotID
			file := filepath.Join(m.state, "generations", old, "index.db")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			state := m.state
			if err = m.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), testPublicationTimeout)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPublicationCrashChild$", "-test.timeout=20s")
			command.Env = append(os.Environ(), "DISKMAP_PUBLICATION_TEST_STATE="+state, "DISKMAP_PUBLICATION_TEST_PHASE="+phase)
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || !exit.Sys().(syscall.WaitStatus).Signaled() || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("expected SIGKILL: %v %s", err, output)
			}
			restored, err := Open(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			after, err := os.ReadFile(file)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("last good bytes changed", err)
			}
			view, err := restored.Snapshot(context.Background(), in.Profile.ID)
			if err != nil || view.SnapshotID == nil {
				t.Fatal(err)
			}
			if phase == "after_commit" {
				if *view.SnapshotID == old {
					t.Fatal("committed generation lost")
				}
			} else if *view.SnapshotID != old {
				t.Fatal("uncommitted generation published")
			}
			var unfinished int
			if err = restored.db.QueryRow(`SELECT count(*) FROM jobs WHERE state IN ('scanning','aggregating','validating','publishing')`).Scan(&unfinished); err != nil || unfinished != 0 {
				t.Fatal("unfinished state after restart", unfinished, err)
			}
		})
	}
}

// TestPublicationCommitBoundary keeps cancellation and post-commit observer failures unambiguous.
func TestPublicationCommitBoundary(t *testing.T) {
	for _, cancelBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel-before", false: "observer-after"}[cancelBefore], func(t *testing.T) {
			m, in, _ := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			m.publishHook = func(phase string) error {
				if cancelBefore && phase == "before_commit" {
					m.cancel()
				}
				if !cancelBefore && phase == "after_commit" {
					return syscall.EIO
				}
				return nil
			}
			s, err := m.Start(in.Profile.ID, "full")
			if err != nil {
				t.Fatal(err)
			}
			out := terminal(t, m, s.Job.ID)
			if cancelBefore {
				if out.Job.State != "interrupted" || out.Job.ResultSnapshotID != nil {
					t.Fatal(out)
				}
			} else if out.Job.State != "completed" || out.Job.ResultSnapshotID == nil {
				t.Fatal(out)
			}
		})
	}
}
