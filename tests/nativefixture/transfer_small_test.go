package nativefixture

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// smallSource includes positive numeric locators, exact large bytes and canonical typed metadata.
func smallSource(t *testing.T) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "index.db")
	db, e := index.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = index.InitializeGeneration(ctx, db, "fixture"); e != nil {
		t.Fatal(e)
	}
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	w, e := index.NewNodeWriter(ctx, tx, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	rid, e := w.AddRoot("volume", "volume", nil)
	if e != nil {
		t.Fatal(e)
	}
	root, e := w.Insert(rid, 0, []byte("."), "directory", nil, nil, "fixture-object", nil)
	if e != nil {
		t.Fatal(e)
	}
	size := contracts.Bytes("9007199254740993")
	if _, e = w.Insert(rid, root, []byte("needle.txt"), "file", &size, nil, "fixture-object", nil); e != nil {
		t.Fatal(e)
	}
	w.Close()
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); e != nil {
		t.Fatal(e)
	}
	if e = index.BuildDerivedProjections(ctx, db, index.DefaultFileTypeMapping()); e != nil {
		t.Fatal(e)
	}
	h := index.GenerationHeader{AggregateVersion: index.AggregateVersion, FileTypeMapping: index.DefaultFileTypeMapping(), SnapshotID: "fixture", JobID: "old-job", Profile: contracts.Profile{ID: "old-profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-10T00:00:00Z", EndedAt: "2026-10-10T00:01:00Z", ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: 2, LogicalBytes: size}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	if e = index.FinalizeGeneration(ctx, db, h); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	hash, e := hashHeld(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	return p, hash
}
func smallOptions(t *testing.T) transferOptions {
	t.Helper()
	source, sha := smallSource(t)
	return transferOptions{Source: source, ExpectedSHA: sha, Output: filepath.Join(t.TempDir(), "new"), ProfileID: "new-profile", JobID: "new-job", Name: "Synthetic test fixture", Timeout: time.Minute, Reserve: 1}
}

// TestTransferPreservesSealedProjection proves the fresh output can serve typed metadata with unchanged numeric locators.
func TestTransferPreservesSealedProjection(t *testing.T) {
	o := smallOptions(t)
	r, e := transfer(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	if !r.Verified || !r.Finalized || !r.SourceUnchanged || r.OutputSHA == r.SourceSHA || r.Entries != 2 {
		t.Fatal(r)
	}
	db, e := index.Open(filepath.Join(o.Output, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var row int64
	var parent string
	var size string
	if e = db.QueryRow(`SELECT rowid,parent_id,logical_bytes FROM nodes WHERE name_bytes=?`, []byte("needle.txt")).Scan(&row, &parent, &size); e != nil || row != 2 || parent != "1" || size != "9007199254740993" {
		t.Fatal(row, parent, size, e)
	}

	h, e := index.ReadGenerationHeader(context.Background(), db, "fixture")
	if e != nil || h.Profile.Name != o.Name || h.Profile.Active || h.Profile.ID != o.ProfileID || h.JobID != o.JobID {
		t.Fatal(h, e)
	}
	for _, q := range []string{`DELETE FROM nodes`, `UPDATE generation_header SET header_json='{}'`} {
		if _, e = db.Exec(q); e == nil {
			t.Fatal("seal bypass", q)
		}
	}
	if _, e = transfer(context.Background(), o); e == nil {
		t.Fatal("existing output accepted")
	}
}

// TestTransferRejectsMissingDerivedData leaves no successful header after corrupted copies.
func TestTransferRejectsMissingDerivedData(t *testing.T) {
	for _, q := range []string{`DELETE FROM nodes WHERE id=2`, `DELETE FROM directory_aggregates`} {
		t.Run(q, func(t *testing.T) {
			o := smallOptions(t)
			o.afterCopy = func(db *sql.DB) error { _, e := db.Exec(q); return e }
			if _, e := transfer(context.Background(), o); e == nil {
				t.Fatal("corruption accepted")
			}
			db, e := index.Open(filepath.Join(o.Output, "index.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var n int
			if e = db.QueryRow("SELECT count(*) FROM generation_header").Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}

// TestTransferFailClosed covers source replacement, deadline, reserve and unsafe source inputs.
func TestTransferFailClosed(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		o := smallOptions(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, e := transfer(ctx, o); e == nil {
			t.Fatal("cancel ignored")
		}
		if _, e := os.Stat(o.Output); !os.IsNotExist(e) {
			t.Fatal("output created")
		}
	})
	t.Run("reserve", func(t *testing.T) {
		o := smallOptions(t)
		o.freeBytes = func(string) (uint64, error) { return 0, nil }
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("reserve ignored")
		}
	})
	t.Run("hash", func(t *testing.T) {
		o := smallOptions(t)
		o.ExpectedSHA = "0000000000000000000000000000000000000000000000000000000000000000"
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("hash ignored")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		o := smallOptions(t)
		link := filepath.Join(t.TempDir(), "link")
		if e := os.Symlink(o.Source, link); e != nil {
			t.Fatal(e)
		}
		o.Source = link
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("symlink followed")
		}
	})
	t.Run("source replacement", func(t *testing.T) {
		o := smallOptions(t)
		o.afterCopy = func(*sql.DB) error {
			if e := os.Rename(o.Source, o.Source+".saved"); e != nil {
				return e
			}
			b, e := os.ReadFile(o.Source + ".saved")
			if e != nil {
				return e
			}
			return os.WriteFile(o.Source, b, 0600)
		}
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("replacement ignored")
		}
	})
}

// TestTransferMidflightBounds verifies that copying cannot escape caller cancellation or later low space.
func TestTransferMidflightBounds(t *testing.T) {
	t.Run("parent cancellation after copy", func(t *testing.T) {
		o := smallOptions(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		o.afterCopy = func(*sql.DB) error { cancel(); return nil }
		if _, e := transfer(ctx, o); e == nil {
			t.Fatal("midflight cancellation ignored")
		}
	})
	t.Run("reserve after copy", func(t *testing.T) {
		o := smallOptions(t)
		var low atomic.Bool
		o.freeBytes = func(string) (uint64, error) {
			if low.Load() {
				return 0, nil
			}
			return 1 << 40, nil
		}
		o.afterCopy = func(*sql.DB) error { low.Store(true); return nil }
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("midflight reserve ignored")
		}
	})
	t.Run("source bytes changed", func(t *testing.T) {
		o := smallOptions(t)
		o.afterCopy = func(*sql.DB) error {
			f, e := os.OpenFile(o.Source, os.O_APPEND|os.O_WRONLY, 0)
			if e != nil {
				return e
			}
			_, e = f.Write([]byte("fault-injection"))
			ce := f.Close()
			if e != nil {
				return e
			}
			return ce
		}
		if _, e := transfer(context.Background(), o); e == nil {
			t.Fatal("changed bytes accepted")
		}
	})
}
