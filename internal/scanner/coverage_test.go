package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// TestSourceReasons distinguishes observed failures without retaining private diagnostic strings.
func TestSourceReasons(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{syscall.EACCES, "SOURCE_UNREADABLE"}, {syscall.ENOENT, "SOURCE_MISSING"}, {syscall.ESTALE, "SOURCE_TRANSIENT"}, {syscall.EIO, "SOURCE_IO_ERROR"}, {errSourceChanged, "SOURCE_CHANGED"}, {errMountChanged, "OUTSIDE_SCOPE_MOUNT"}, {ErrTraversalDepth, "DEPTH_LIMIT"}, {io.ErrNoProgress, "SOURCE_IO_ERROR"},
	} {
		if got := sourceReason(&os.PathError{Op: "stat", Path: "private-name", Err: tc.err}); got != tc.code {
			t.Fatal(got, tc.code)
		}
	}
}

// coverageFixture captures two independent roots so a failed area cannot suppress its healthy peer.
func coverageFixture(t *testing.T) (contracts.Profile, scopemanifest.ScopeManifest) {
	t.Helper()
	p := contracts.Profile{ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}, ID: "profile", Revision: 1, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"first", "second"}}}
	m := scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "shares"}
	for _, id := range p.Scope.TargetIDs {
		dir, e := filepath.EvalSymlinks(t.TempDir())
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "file"), []byte("data"), 0600); e != nil {
			t.Fatal(e)
		}
		r, b, e := OpenBoundRoot(dir)
		if e != nil {
			t.Fatal(e)
		}
		r.Close()
		m.Roots = append(m.Roots, scopemanifest.ManifestRoot{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: id, Kind: "share", VolumeID: "volume", PhysicalPath: dir, State: "reachable"}, Binding: &scopemanifest.RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}})
	}
	return p, m
}

// TestObservedGapsRemainLocal exercises injected syscall failures through the production scan visitor.
func TestObservedGapsRemainLocal(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{{syscall.EACCES, "SOURCE_UNREADABLE"}, {syscall.ENOENT, "SOURCE_MISSING"}, {syscall.ESTALE, "SOURCE_TRANSIENT"}, {errSourceChanged, "SOURCE_CHANGED"}, {errMountChanged, "OUTSIDE_SCOPE_MOUNT"}} {
		t.Run(tc.code, func(t *testing.T) {
			p, m := coverageFixture(t)
			before, _ := json.Marshal(m)
			db, e := index.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			first := true
			result, e := scanManifestWithOperations(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping(), walkOperations{inspect: func(f *os.File, n, mount string) (*os.File, os.FileInfo, error) {
				if first {
					first = false
					return nil, nil, tc.err
				}
				return inspectEntry(f, n, mount)
			}, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }})
			after, _ := json.Marshal(m)
			if e != nil || result.Errors != 1 || result.Entries != 3 || result.Bytes != "4" || string(before) != string(after) {
				t.Fatal(result, e)
			}
			var code, state string
			if e = db.QueryRow(`SELECT reason_code FROM scan_boundaries`).Scan(&code); e != nil || code != tc.code {
				t.Fatal(code, e)
			}
			if e = db.QueryRow(`SELECT state FROM coverage WHERE target_id='second'`).Scan(&state); e != nil || state != "complete" {
				t.Fatal(state, e)
			}
			if _, e = index.FindNode(context.Background(), db, "first", []byte("file")); !errors.Is(e, sql.ErrNoRows) {
				t.Fatal("missing node result", e)
			}
		})
	}
}

// TestMissingAndLockedRootsRetainManifest keeps absent sources distinct from empty directories.
func TestMissingAndLockedRootsRetainManifest(t *testing.T) {
	for _, state := range []string{"locked", "missing", "unmounted", "unreadable", "unverified"} {
		t.Run(state, func(t *testing.T) {
			p, m := coverageFixture(t)
			m.Roots[0].Binding = nil
			m.Roots[0].State = state
			m.Issues = []scopemanifest.ScopeIssue{{TargetID: "first", State: state, Code: "SCOPE_TARGET"}}
			before, _ := json.Marshal(m)
			db, e := index.Open(":memory:")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			result, e := ScanManifest(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping())
			after, _ := json.Marshal(m)
			if e != nil || result.Errors == 0 || result.Entries != 2 || result.Bytes != "4" || string(before) != string(after) {
				t.Fatal(result, e)
			}
			var code string
			if e = db.QueryRow(`SELECT reason_code FROM scan_boundaries WHERE target_id='first'`).Scan(&code); e != nil || code != sourceStateReason(state) {
				t.Fatal(code, e)
			}
		})
	}
}

// TestReaddirNoProgressAndFailure records partial batches while continuing the next root.
func TestReaddirNoProgressAndFailure(t *testing.T) {
	for _, failure := range []error{nil, syscall.EIO} {
		p, m := coverageFixture(t)
		db, e := index.Open(":memory:")
		if e != nil {
			t.Fatal(e)
		}
		first := true
		r, e := scanManifestWithOperations(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping(), walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
			if first {
				first = false
				return nil, failure
			}
			return f.ReadDir(n)
		}})
		if e != nil || r.Errors != 1 || r.Entries != 3 || r.Bytes != "4" {
			t.Fatal(r, e)
		}
		db.Close()
	}
}

// TestGapCancellationRemainsFatal prevents cancellation from becoming a publishable source error.
func TestGapCancellationRemainsFatal(t *testing.T) {
	p, m := coverageFixture(t)
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = ScanManifest(ctx, db, "snapshot", p, m, index.DefaultFileTypeMapping()); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

// TestSourceRootDisappearsAfterCapture continues healthy roots and records the exact known cause.
func TestSourceRootDisappearsAfterCapture(t *testing.T) {
	p, m := coverageFixture(t)
	if err := os.Rename(m.Roots[0].PhysicalPath, m.Roots[0].PhysicalPath+"-moved"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(m.Roots[0].PhysicalPath+"-moved", m.Roots[0].PhysicalPath)
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	r, e := ScanManifest(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping())
	if e != nil || r.Errors != 1 || r.Entries != 2 || r.Bytes != "4" {
		t.Fatal(r, e)
	}
	var code string
	if e = db.QueryRow(`SELECT reason_code FROM scan_boundaries WHERE target_id='first'`).Scan(&code); e != nil || code != "SOURCE_MISSING" {
		t.Fatal(code, e)
	}
}

// TestKnownExcludedRecordsKeepCompleteCoverage distinguishes configured exclusions from failures.
func TestKnownExcludedRecordsKeepCompleteCoverage(t *testing.T) {
	p, m := coverageFixture(t)
	p.Exclusions = []contracts.Exclusion{{Kind: "path", Value: "file"}}
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	r, e := ScanManifest(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping())
	if e != nil || r.Errors != 0 || r.Entries != 2 {
		t.Fatal(r, e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM scan_boundaries WHERE reason_code='PROFILE_EXCLUDED'`).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}

// TestUnlinkedHeldFileIsMissing catches the real fstat-after-unlink metadata race.
func TestUnlinkedHeldFileIsMissing(t *testing.T) {
	p, m := coverageFixture(t)
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	first := true
	r, e := scanManifestWithOperations(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping(), walkOperations{inspect: func(f *os.File, n, mount string) (*os.File, os.FileInfo, error) {
		if !first {
			return inspectEntry(f, n, mount)
		}
		first = false
		held, e := os.Open(filepath.Join(m.Roots[0].PhysicalPath, n))
		if e != nil {
			return nil, nil, e
		}
		// Only this test fixture opens a payload handle; production uses metadata-only handles.
		if e = os.Remove(filepath.Join(m.Roots[0].PhysicalPath, n)); e != nil {
			held.Close()
			return nil, nil, e
		}
		info, e := held.Stat()
		return held, info, e
	}, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }})
	if e != nil || r.Errors != 1 || r.Entries != 3 || r.Bytes != "4" {
		t.Fatal(r, e)
	}
	var code string
	if e = db.QueryRow(`SELECT reason_code FROM scan_boundaries`).Scan(&code); e != nil || code != "SOURCE_MISSING" {
		t.Fatal(code, e)
	}
}
