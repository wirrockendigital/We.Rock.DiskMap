package index

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"testing"
)

// Unreadable and intentionally excluded trees are unknown, while unrelated empty folders remain zero.
func TestDirectorySizeUnmeasured(t *testing.T) {
	db, _ := generationFixture(t)
	ctx := context.Background()
	for _, boundary := range []struct{ path, code string }{{"share/#recycle", "SOURCE_UNREADABLE"}, {"share/#snapshot", "SNAPSHOT_TREE_EXCLUDED"}} {
		if _, err := db.Exec(`INSERT INTO scan_boundaries VALUES(?,?,?)`, "volume", []byte(boundary.path), boundary.code); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		target, path string
		want         bool
	}{{"volume", ".", true}, {"volume", "share", true}, {"volume", "share/#recycle", true}, {"volume", "share/#snapshot", true}, {"volume", "share/empty", false}, {"volume", "shar", false}, {"other", "share/#recycle", false}} {
		got, err := DirectorySizeUnmeasured(ctx, db, tc.target, []byte(tc.path))
		if err != nil || got != tc.want {
			t.Fatalf("%+v: got=%v err=%v", tc, got, err)
		}
	}
}

// TestBoundarySealAndCoverage rejects false-complete gaps and protects records after publication.
func TestBoundarySealAndCoverage(t *testing.T) {
	ctx := context.Background()
	for _, code := range []string{"PACKAGE_STATE_EXCLUDED", "SUBVOLUME_CLASSIFICATION_UNAVAILABLE", "OUTSIDE_SCOPE_MOUNT", "SOURCE_UNREADABLE", "SOURCE_MISSING", "SOURCE_TRANSIENT", "SOURCE_CHANGED", "SOURCE_IO_ERROR", "SOURCE_LOCKED", "SOURCE_NOT_MOUNTED", "SOURCE_UNVERIFIED", "DEPTH_LIMIT", "PROFILE_EXCLUDED", "SCOPE_CHANGED", "SCOPE_VALIDATION_UNAVAILABLE"} {
		t.Run(code, func(t *testing.T) {
			db, h := generationFixture(t)
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := RecordBoundary(ctx, tx, "volume", "boundary", code); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if BoundaryIsGap(code) {
				if FinalizeGeneration(ctx, db, h) == nil {
					t.Fatal("complete gap sealed")
				}
				h.State = "partial"
				h.CoverageErrors = 1
				if _, err := db.Exec(`UPDATE coverage SET state='partial',errors=1`); err != nil {
					t.Fatal(err)
				}
			}
			if err := FinalizeGeneration(ctx, db, h); err != nil {
				t.Fatal(err)
			}
			got, err := ReadGenerationHeader(ctx, db, h.SnapshotID)
			if err != nil || got.ScanBoundaryCount != "1" {
				t.Fatal(got, err)
			}
			if _, err := db.Exec(`DELETE FROM scan_boundaries`); err == nil {
				t.Fatal("sealed boundary mutated")
			}
		})
	}
}

// TestCoveredIssueBoundaryQualification rejects an injected foreign target while admitting captured gaps.
func TestCoveredIssueBoundaryQualification(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		db, h := generationFixture(t)
		h.Manifest.Issues = append(h.Manifest.Issues, scopemanifest.ScopeIssue{TargetID: "missing-share", State: "locked", Code: "SCOPE_TARGET_LOCKED"})
		if qualified {
			h.Manifest.CoveredTargetIDs = append(h.Manifest.CoveredTargetIDs, "missing-share")
		}
		h.State = "partial"
		h.CoverageErrors = 1
		if _, e := db.Exec(`INSERT INTO coverage VALUES('missing-share','partial',1)`); e != nil {
			t.Fatal(e)
		}
		tx, e := db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		if e = RecordBoundary(context.Background(), tx, "missing-share", ".", "SOURCE_LOCKED"); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		e = FinalizeGeneration(context.Background(), db, h)
		if (e == nil) != qualified {
			t.Fatal(qualified, e)
		}
	}
}
