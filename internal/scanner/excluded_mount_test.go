package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// TestExcludedMountBoundary drives the real visitor with no-follow metadata and a rejected mount.
func TestExcludedMountBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, kind, rule, object string
		unknown, excluded        bool
	}{
		{"wildcard", "pattern", "*", "directory", false, true},
		{"literal", "path", "blocked", "directory", false, true},
		{"directory", "pattern", "blocked/", "directory", false, true},
		{"unmatched", "pattern", "other", "directory", false, false},
		{"unknown-type", "pattern", "blocked/", "directory", true, false},
		{"unknown-wildcard", "pattern", "*", "directory", true, false},
		{"symlink-not-directory", "pattern", "blocked/", "symlink", false, false},
		{"file-not-directory", "pattern", "blocked/", "file", false, false},
		{"symlink-excluded", "pattern", "*", "symlink", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, m := coverageFixture(t)
			p.Scope.TargetIDs = p.Scope.TargetIDs[:1]
			m.Roots = m.Roots[:1]
			root := m.Roots[0].PhysicalPath
			if err := os.Remove(filepath.Join(root, "file")); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "blocked")
			switch tc.object {
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "never-read"), []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("missing-directory", target); err != nil {
					t.Fatal(err)
				}
			}
			p.Exclusions = []contracts.Exclusion{{Kind: tc.kind, Value: tc.rule}}
			db, err := index.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			inspections, reads := 0, 0
			result, err := scanManifestWithOperations(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping(), walkOperations{
				inspect: func(f *os.File, name, mount string) (*os.File, os.FileInfo, error) {
					inspections++
					if name != "blocked" {
						t.Fatal("unexpected descendant inspection")
					}
					if tc.unknown {
						return nil, nil, errMountChanged
					}
					info, e := os.Lstat(target)
					if e != nil {
						t.Fatal(e)
					}
					return nil, info, errMountChanged
				},
				directory: func(*os.File, string, os.FileInfo, string) (*os.File, error) {
					t.Fatal("rejected mount opened for traversal")
					return nil, nil
				},
				readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { reads++; return f.ReadDir(n) },
			})
			if err != nil {
				t.Fatal(err)
			}
			reason, wantState, wantErrors := "OUTSIDE_SCOPE_MOUNT", "partial", 1
			if tc.excluded {
				reason, wantState, wantErrors = "PROFILE_EXCLUDED", "complete", 0
			}
			var gotReason, state string
			if err = db.QueryRow(`SELECT reason_code FROM scan_boundaries`).Scan(&gotReason); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT state FROM coverage`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if gotReason != reason || state != wantState || result.Errors != uint64(wantErrors) || result.Entries != 1 || result.Bytes != "0" || inspections != 1 || reads > 2 {
				t.Fatalf("reason=%s state=%s result=%+v inspections=%d reads=%d", gotReason, state, result, inspections, reads)
			}
		})
	}
}
