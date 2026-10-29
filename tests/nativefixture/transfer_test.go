// Package nativefixture contains opt-in fixture tooling, never runtime import routes.
package nativefixture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"golang.org/x/sys/unix"
)

type transferOptions struct {
	Source, Output, ExpectedSHA, ProfileID, JobID, Name string
	Timeout                                             time.Duration
	Reserve                                             uint64
	// Hooks are private fault injection for small tests; the opt-in runner supplies none.
	afterCopy func(*sql.DB) error
	progress  func(string)
	freeBytes func(string) (uint64, error)
}
type transferReceipt struct {
	SourceSHA, OutputSHA, SnapshotID, SourceProfileID, SourceJobID, ProfileID, JobID string
	Entries                                                                          uint64
	SourceSchema, TargetSchema                                                       int
	SourceProfile, DerivedProfile                                                    contracts.Profile
	SourceUnchanged, Finalized, Verified                                             bool
}

// hashHeld is bounded in memory and cooperatively observes the same overall deadline.
func hashHeld(ctx context.Context, f *os.File) (string, error) {
	if _, e := f.Seek(0, 0); e != nil {
		return "", e
	}
	h := sha256.New()
	b := make([]byte, 1<<20)
	for {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		n, e := f.Read(b)
		if n > 0 {
			h.Write(b[:n])
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// readonlyURI keeps SQLite attached to the held no-follow file rather than a replaceable name.
func readonlyURI(f *os.File) string {
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	u := url.URL{Scheme: "file", Path: prefix + strconv.Itoa(int(f.Fd()))}
	return u.String() + "?mode=ro&immutable=1"
}
func available(path string) (uint64, error) {
	var s unix.Statfs_t
	e := unix.Statfs(path, &s)
	return uint64(s.Bavail) * uint64(s.Bsize), e
}

// transfer creates a new database, preserving canonical row identities and typed metadata.
// An error leaves an explicitly incomplete private output, never a success receipt or Control pointer.
func transfer(parent context.Context, o transferOptions) (receipt transferReceipt, err error) {
	if o.Timeout <= 0 || len(o.ExpectedSHA) != 64 || !security.ValidID(o.ProfileID) || !security.ValidID(o.JobID) || o.Name == "" || !filepath.IsAbs(o.Source) || !filepath.IsAbs(o.Output) {
		return receipt, errors.New("INVALID_TRANSFER_OPTIONS")
	}
	report := func(stage string) {
		if o.progress != nil {
			o.progress(stage)
		}
	}
	report("source-hash-before")
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return receipt, e
	}
	fd, e := unix.Open(o.Source, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return receipt, e
	}
	f := os.NewFile(uintptr(fd), o.Source)
	defer f.Close()
	original, e := f.Stat()
	if e != nil || !original.Mode().IsRegular() {
		return receipt, errors.New("INVALID_SOURCE")
	}
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, e = os.Lstat(o.Source + suffix); !os.IsNotExist(e) {
			return receipt, errors.New("SOURCE_NOT_OFFLINE")
		}
	}
	before, e := hashHeld(ctx, f)
	if e != nil {
		return receipt, e
	}
	if before != o.ExpectedSHA {
		return receipt, errors.New("SOURCE_SHA_MISMATCH")
	}
	src, e := sql.Open("sqlite", readonlyURI(f))
	if e != nil {
		return receipt, e
	}
	defer src.Close()
	src.SetMaxOpenConns(1)
	var snapshot string
	if e = src.QueryRowContext(ctx, "SELECT snapshot_id FROM generation_identity WHERE singleton=1").Scan(&snapshot); e != nil {
		return receipt, e
	}
	h, e := readTransferHeader(ctx, src, snapshot)
	if e != nil {
		return receipt, e
	}
	if h.Manifest.Provenance.Authority != "synthetic-fixture" || h.State != "complete" || h.Profile.ID == o.ProfileID || h.JobID == o.JobID {
		return receipt, errors.New("INVALID_FIXTURE_AUTHORITY")
	}
	sourceProfile := h.Profile
	receipt = transferReceipt{SourceSHA: before, SnapshotID: snapshot, SourceProfileID: h.Profile.ID, SourceJobID: h.JobID, ProfileID: o.ProfileID, JobID: o.JobID, Entries: h.Entries, SourceSchema: h.SchemaVersion, TargetSchema: index.GenerationSchema}
	known := map[string]bool{}
	targets := []string{}
	for _, r := range h.Manifest.Roots {
		known[r.TargetID] = true
		targets = append(targets, r.TargetID)
	}
	h.Profile = contracts.Profile{ID: o.ProfileID, Revision: 1, Name: o.Name, Scope: contracts.Scope{Kind: h.Manifest.Kind, TargetIDs: targets}, Active: false, Exclusions: []contracts.Exclusion{}, ResourceMode: "balanced", Limits: contracts.ResourceLimits{Workers: 1}}
	if e = profiles.Validate(h.Profile, known); e != nil {
		return receipt, e
	}
	h.JobID = o.JobID
	// The destination is a newly derived generation, not an in-place schema upgrade.
	h.SchemaVersion = index.GenerationSchema
	receipt.SourceProfile = sourceProfile
	receipt.DerivedProfile = h.Profile
	free := o.freeBytes
	if free == nil {
		free = available
	}
	check := func() error {
		if e := ctx.Err(); e != nil {
			return e
		}
		n, e := free(filepath.Dir(o.Output))
		if e != nil {
			return e
		}
		if n < o.Reserve {
			return errors.New("FIXTURE_RESERVE_LOW")
		}
		return nil
	}
	if e = check(); e != nil {
		return receipt, e
	}
	// Preflight allows two source-file sizes above the reserve for output and journal growth.
	// Runtime checks remain authoritative; this allowance is not a guaranteed peak estimate.
	n, e := free(filepath.Dir(o.Output))
	if e != nil {
		return receipt, e
	}
	if original.Size() < 0 || uint64(original.Size()) > (^uint64(0)-o.Reserve)/2 || n < o.Reserve+2*uint64(original.Size()) {
		return receipt, errors.New("FIXTURE_PEAK_ALLOWANCE_LOW")
	}
	report("initialize-exclusive-output")
	// The output directory is exclusively created; no existing file or directory is replaced.
	if e = os.Mkdir(o.Output, 0700); e != nil {
		return receipt, e
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if check() != nil {
					cancel()
					return
				}
			}
		}
	}()
	dest := filepath.Join(o.Output, "index.db")
	file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return receipt, e
	}
	if e = file.Close(); e != nil {
		return receipt, e
	}
	db, e := index.Open(dest)
	if e != nil {
		return receipt, e
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	if _, e = db.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL; PRAGMA cache_size=-8192; PRAGMA temp_store=FILE; PRAGMA mmap_size=0"); e != nil {
		return receipt, e
	}
	if e = index.InitializeGeneration(ctx, db, snapshot); e != nil {
		return receipt, e
	}
	if _, e = db.ExecContext(ctx, "ATTACH DATABASE ? AS fixture", readonlyURI(f)); e != nil {
		return receipt, e
	}
	// Every copied table must match the current physical schema exactly.
	tables := []string{"roots", "nodes", "coverage", "scan_boundaries", "directory_aggregates", "aggregate_mapping"}
	for _, table := range tables {
		var a, b string
		if e = db.QueryRowContext(ctx, "SELECT sql FROM main.sqlite_schema WHERE name=?", table).Scan(&a); e != nil {
			return receipt, e
		}
		if e = db.QueryRowContext(ctx, "SELECT sql FROM fixture.sqlite_schema WHERE name=?", table).Scan(&b); e != nil {
			return receipt, e
		}
		if a != b {
			return receipt, errors.New("FIXTURE_SCHEMA_MISMATCH")
		}
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return receipt, e
	}
	defer tx.Rollback()
	for _, table := range tables {
		report("copy-" + table)
		if e = check(); e != nil {
			return receipt, e
		}
		columns := "*"
		destination := table
		if _, e = tx.ExecContext(ctx, "INSERT INTO main."+destination+" SELECT "+columns+" FROM fixture."+table); e != nil {
			return receipt, fmt.Errorf("copy %s: %w", table, e)
		}
	}
	if e = tx.Commit(); e != nil {
		return receipt, e
	}
	if o.afterCopy != nil {
		if e = o.afterCopy(db); e != nil {
			return receipt, e
		}
	}
	if e = db.Close(); e != nil {
		return receipt, e
	}
	// Reopen the copied database before the complete production seal validation.
	db, e = index.Open(dest)
	if e != nil {
		return receipt, e
	}
	if _, e = db.ExecContext(ctx, "PRAGMA synchronous=FULL; PRAGMA cache_size=-8192; PRAGMA temp_store=FILE; PRAGMA mmap_size=0"); e != nil {
		return receipt, e
	}
	if e = check(); e != nil {
		return receipt, e
	}
	report("finalize-full")
	if e = index.FinalizeGeneration(ctx, db, h); e != nil {
		return receipt, e
	}
	receipt.Finalized = true
	if e = db.Close(); e != nil {
		return receipt, e
	}
	out, e := os.Open(dest)
	if e != nil {
		return receipt, e
	}
	defer out.Close()
	verify, e := sql.Open("sqlite", readonlyURI(out))
	if e != nil {
		return receipt, e
	}
	verify.SetMaxOpenConns(1)
	report("verify-full-readonly")
	e = index.VerifyGeneration(ctx, verify, snapshot, o.ProfileID, o.JobID)
	ce := verify.Close()
	if e != nil {
		return receipt, e
	}
	if ce != nil {
		return receipt, ce
	}
	receipt.Verified = true
	report("output-hash")
	receipt.OutputSHA, e = hashHeld(ctx, out)
	if e != nil {
		return receipt, e
	}
	report("source-hash-after")
	after, e := hashHeld(ctx, f)
	if e != nil {
		return receipt, e
	}
	current, e := os.Lstat(o.Source)
	if e != nil {
		return receipt, e
	}
	// A newly appearing journal also invalidates the immutable source assumption.
	for _, suffix := range []string{"-wal", "-journal"} {
		if _, e = os.Lstat(o.Source + suffix); !os.IsNotExist(e) {
			return receipt, errors.New("SOURCE_NOT_OFFLINE")
		}
	}
	if before != after || !os.SameFile(original, current) || original.Size() != current.Size() || !original.ModTime().Equal(current.ModTime()) {
		return receipt, errors.New("SOURCE_CHANGED")
	}
	if e = check(); e != nil {
		return receipt, e
	}
	receipt.SourceUnchanged = true
	return receipt, nil
}

// TestTransferNativeFixture is explicitly opt-in; normal test runs construct only small fixtures.
func TestTransferNativeFixture(t *testing.T) {
	source := os.Getenv("DISKMAP_TRANSFER_SOURCE")
	if source == "" {
		t.Skip("explicit immutable source and exclusive output required")
	}
	minutes := 1440
	if raw := os.Getenv("DISKMAP_TRANSFER_TIMEOUT_MINUTES"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 30 || v > 1440 {
			t.Fatal("timeout must be 30..1440 minutes")
		}
		minutes = v
	}
	started := time.Now()
	receipt, e := transfer(context.Background(), transferOptions{progress: func(stage string) { t.Logf("transfer stage=%s elapsed=%s", stage, time.Since(started)) }, Source: source, Output: os.Getenv("DISKMAP_TRANSFER_OUTPUT"), ExpectedSHA: os.Getenv("DISKMAP_TRANSFER_SHA256"), ProfileID: os.Getenv("DISKMAP_TRANSFER_PROFILE_ID"), JobID: os.Getenv("DISKMAP_TRANSFER_JOB_ID"), Name: os.Getenv("DISKMAP_TRANSFER_NAME"), Timeout: time.Duration(minutes) * time.Minute, Reserve: 64 << 30})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(receipt)
	t.Log(string(b))
}

// readTransferHeader admits only current-schema test sources; old layouts require a new fixture.
// Production readers remain current-schema-only; SHA qualification and full destination validation are mandatory.
func readTransferHeader(ctx context.Context, db *sql.DB, snapshot string) (index.GenerationHeader, error) {
	var h index.GenerationHeader
	var version int
	var raw string
	if index.GenerationSchema != 13 {
		return h, errors.New("TRANSFER_TARGET_NOT_QUALIFIED")
	}
	if e := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil {
		return h, e
	}
	if version != index.GenerationSchema {
		return h, errors.New("TRANSFER_SOURCE_SCHEMA")
	}
	if e := db.QueryRowContext(ctx, "SELECT header_json FROM generation_header WHERE singleton=1").Scan(&raw); e != nil {
		return h, e
	}
	if len(raw) > 1<<20 {
		return h, errors.New("TRANSFER_HEADER_BUDGET")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&h); e != nil {
		return h, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF || h.SchemaVersion != version || h.AggregateVersion != index.AggregateVersion || h.ScannerVersion != index.ScannerVersion || h.SnapshotID != snapshot || h.State != "complete" || h.Manifest.Provenance.Authority != "synthetic-fixture" || h.ScanBoundaryCount.Validate() != nil || index.ValidateSourceProvenance(h.Manifest) != nil {
		return h, errors.New("TRANSFER_HEADER_INVALID")
	}
	return h, nil
}
