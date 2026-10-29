package querybench

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// fileHash records the immutable fixture source without retaining its contents in memory.
func fileHash(t *testing.T, path string) string {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		t.Fatal(e)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestBuildProductionScale copies canonical synthetic rows into a fresh current-schema generation.
// It never upgrades or writes the old fixture and deliberately retains the output for independent replay.
func TestBuildProductionScale(t *testing.T) {
	source, base := os.Getenv("DISKMAP_SCALE_SOURCE"), os.Getenv("DISKMAP_SCALE_OUTPUT")
	if source == "" || base == "" {
		t.Skip("explicit local immutable source and new output directory required")
	}
	minutes, e := scaleBuildBound(os.Getenv("DISKMAP_SCALE_TIMEOUT_MINUTES"), 180, 30, 1440)
	if e != nil {
		t.Fatal(e)
	}
	reserveGiB, e := scaleBuildBound(os.Getenv("DISKMAP_SCALE_RESERVE_GIB"), 64, 64, 512)
	if e != nil {
		t.Fatal(e)
	}
	mode, syncMode, e := scaleJournalPolicy(os.Getenv("DISKMAP_SCALE_JOURNAL"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(base, 0700); e != nil {
		t.Fatal("output must be new", e)
	}
	// Check the reserve before the first database write, as well as during construction.
	var initialSpace syscall.Statfs_t
	if e = syscall.Statfs(base, &initialSpace); e != nil {
		t.Fatal(e)
	}
	if uint64(initialSpace.Bavail)*uint64(initialSpace.Bsize) < uint64(reserveGiB)<<30 {
		t.Fatal("fixture output would violate the required free-space reserve")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(minutes)*time.Minute)
	defer cancel()
	// Fixture construction preserves the explicit host reserve; cancellation leaves only this new output.
	go func() {
		timer := time.NewTicker(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				var fs syscall.Statfs_t
				if err := syscall.Statfs(base, &fs); err != nil || uint64(fs.Bavail)*uint64(fs.Bsize) < uint64(reserveGiB)<<30 {
					cancel()
					return
				}
			}
		}
	}()
	u := url.URL{Scheme: "file", Path: source}
	sourceURI := u.String() + "?mode=ro"
	original, e := sql.Open("sqlite", sourceURI)
	if e != nil {
		t.Fatal(e)
	}
	defer original.Close()
	var raw string
	if e = original.QueryRowContext(ctx, "SELECT header_json FROM generation_header WHERE singleton=1").Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var h index.GenerationHeader
	if json.Unmarshal([]byte(raw), &h) != nil || h.Entries != 10000000 || h.State != "complete" {
		t.Fatal("sealed 10M source required")
	}
	sourceSchema := h.SchemaVersion
	before := fileHash(t, source)
	t.Logf("source=%s source_schema=%d entries=%d sha256=%s output=%s", source, h.SchemaVersion, h.Entries, before, base)
	db, e := index.Open(filepath.Join(base, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = configureScaleJournal(ctx, db, mode); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, "PRAGMA cache_size=-8192;PRAGMA temp_store=FILE"); e != nil {
		t.Fatal(e)
	}
	t.Logf("fixture_journal=%s synchronous=%s", mode, syncMode)
	if e = index.InitializeGeneration(ctx, db, h.SnapshotID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, "ATTACH DATABASE ? AS fixture", sourceURI); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	// All canonical columns are copied unchanged; derived tables are built by production functions below.
	for _, table := range []string{"nodes", "coverage", "scan_boundaries"} {
		if _, e = tx.ExecContext(ctx, "INSERT INTO main."+table+" SELECT * FROM fixture."+table); e != nil {
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, "DETACH DATABASE fixture"); e != nil {
		t.Fatal(e)
	}
	t.Logf("canonical_copy_committed elapsed=%s", time.Since(start))
	if e = index.BuildDerivedProjections(ctx, db, h.FileTypeMapping); e != nil {
		t.Fatal(e)
	}
	t.Logf("production_projections_committed elapsed=%s", time.Since(start))
	h.SchemaVersion = index.GenerationSchema
	h.ScannerVersion = index.ScannerVersion
	h.AggregateVersion = index.AggregateVersion
	// Synthetic canonical rows carry explicit fixture authority, never invented native inventory.
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	if e = index.FinalizeGeneration(ctx, db, h); e != nil {
		t.Fatal(e)
	}
	if mode == "WAL" {
		if _, e = db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	after := fileHash(t, source)
	if before != after {
		t.Fatal("source fixture changed")
	}
	info, e := os.Stat(filepath.Join(base, "index.db"))
	if e != nil {
		t.Fatal(e)
	}
	evidence := map[string]any{"journalMode": mode, "synchronous": syncMode, "timeoutMinutes": minutes, "reserveGiB": reserveGiB, "source": source, "sourceSHA256": before, "sourceSchema": sourceSchema, "outputSchema": index.GenerationSchema, "entries": h.Entries, "elapsedSeconds": time.Since(start).Seconds(), "databaseBytes": info.Size(), "createdAt": time.Now().UTC().Format(time.RFC3339Nano)}
	encoded, _ := json.MarshalIndent(evidence, "", "  ")
	if e = os.WriteFile(filepath.Join(base, "fixture-provenance.json"), encoded, 0600); e != nil {
		t.Fatal(e)
	}
	t.Logf("sealed_production_generation elapsed=%s bytes=%d source_unchanged=%t", time.Since(start), info.Size(), before == after)
}

// scaleBuildBound accepts only canonical bounded decimal harness settings.
func scaleBuildBound(raw string, fallback, minimum, maximum int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(n) != raw || n < minimum || n > maximum {
		return 0, fmt.Errorf("invalid bounded scale build setting")
	}
	return n, nil
}

// TestScaleBuildBounds prevents unbounded runtime or a silently weakened disk reserve.
func TestScaleBuildBounds(t *testing.T) {
	for _, raw := range []string{"0", "-1", "29", "1441", "0180", "180m", "99999999999999999999"} {
		if _, err := scaleBuildBound(raw, 180, 30, 1440); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"", "30", "180", "1440"} {
		if _, err := scaleBuildBound(raw, 180, 30, 1440); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"31", "32", "63"} {
		if _, err := scaleBuildBound(raw, 64, 64, 512); err == nil {
			t.Fatal("reserve weakened")
		}
	}
	if n, err := scaleBuildBound("64", 64, 64, 512); err != nil || n != 64 {
		t.Fatal("native reserve rejected")
	}
}

// scaleJournalPolicy accepts only explicitly qualified fixture durability modes.
func scaleJournalPolicy(raw string) (string, string, error) {
	if raw == "" || raw == "WAL" {
		return "WAL", "NORMAL", nil
	}
	if raw == "DELETE" {
		return "DELETE", "FULL", nil
	}
	return "", "", fmt.Errorf("invalid fixture journal mode")
}

// configureScaleJournal changes only the fresh output and verifies the actual SQLite settings.
func configureScaleJournal(ctx context.Context, db *sql.DB, raw string) error {
	mode, syncMode, err := scaleJournalPolicy(raw)
	if err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "PRAGMA main.journal_mode="+mode+";PRAGMA main.synchronous="+syncMode); err != nil {
		return err
	}
	var actual string
	var syncValue int
	if err = db.QueryRowContext(ctx, "PRAGMA main.journal_mode").Scan(&actual); err != nil {
		return err
	}
	if err = db.QueryRowContext(ctx, "PRAGMA main.synchronous").Scan(&syncValue); err != nil {
		return err
	}
	wantSync := 1
	if mode == "DELETE" {
		wantSync = 2
	}
	if strings.ToUpper(actual) != mode || syncValue != wantSync {
		return fmt.Errorf("fixture journal policy was not applied")
	}
	return nil
}

// TestScaleJournalPolicy verifies real file-backed SQLite state without constructing a scale fixture.
func TestScaleJournalPolicy(t *testing.T) {
	for _, invalid := range []string{"OFF", "MEMORY", "delete", "WAL;PRAGMA synchronous=OFF", "TRUNCATE", " "} {
		if _, _, err := scaleJournalPolicy(invalid); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	for _, mode := range []string{"WAL", "DELETE"} {
		t.Run(mode, func(t *testing.T) {
			db, err := index.Open(filepath.Join(t.TempDir(), "policy.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = configureScaleJournal(context.Background(), db, mode); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("CREATE TABLE actual(value INTEGER);INSERT INTO actual VALUES(1)"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err = db.QueryRow("SELECT count(*) FROM actual").Scan(&count); err != nil || count != 1 {
				t.Fatal(err, count)
			}
		})
	}
}
