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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/uninstall"
	"golang.org/x/sys/unix"
)

type registrationOptions struct{ State, Backup, Snapshot, Profile, Job, SHA string }
type registrationReceipt struct {
	State, Backup, BackupSHA, Snapshot, Profile, Job, GenerationSHA, OutcomeJSON, ProfileJSON, Stamp, Binding string
	Baseline                                                                                                  map[string]string
}

// offlineControl takes the exact product singleton lock before any SQLite access.
func offlineControl(ctx context.Context, state string) (*sql.DB, func(), error) {
	if !filepath.IsAbs(state) || filepath.Clean(state) != state {
		return nil, nil, errors.New("INVALID_STATE")
	}
	resolved, e := filepath.EvalSymlinks(state)
	if e != nil || resolved != state {
		return nil, nil, errors.New("ALIASED_STATE")
	}
	lock, e := security.OpenPrivateFile(filepath.Join(state, "daemon.lock"), os.O_RDWR)
	if e != nil {
		return nil, nil, e
	}
	if e = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		lock.Close()
		return nil, nil, e
	}
	fail := func(e error) (*sql.DB, func(), error) { lock.Close(); return nil, nil, e }
	if e = migration.CheckReady(state); e != nil {
		return fail(e)
	}
	if e = uninstall.CheckReady(state); e != nil {
		return fail(e)
	}
	control, e := security.OpenPrivateFile(filepath.Join(state, "control.db"), os.O_RDWR)
	if e != nil {
		return fail(e)
	}
	control.Close()
	db, e := index.Open(filepath.Join(state, "control.db"))
	if e != nil {
		return fail(e)
	}
	close := func() { db.Close(); lock.Close() }
	var version int
	if e = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); e != nil || version != migration.ControlVersion {
		close()
		return nil, nil, errors.New("CONTROL_SCHEMA_MISMATCH")
	}
	var active int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE state NOT IN ('completed','failed','cancelled','interrupted')`).Scan(&active); e != nil || active != 0 {
		close()
		return nil, nil, errors.New("ACTIVE_WORK")
	}
	if _, e = db.ExecContext(ctx, "PRAGMA synchronous=FULL; PRAGMA busy_timeout=1000"); e != nil {
		close()
		return nil, nil, e
	}
	return db, close, nil
}

// recordPredicates enumerates the only five inserted record identities; every other row is invariant.
func recordPredicates(r registrationReceipt) map[string]struct{ field, value string } {
	return map[string]struct{ field, value string }{"profiles": {"id", r.Profile}, "jobs": {"id", r.Job}, "active_generations": {"profile_id", r.Profile}, "integrity_generations": {"snapshot_id", r.Snapshot}, "retention_qualification": {"snapshot_id", r.Snapshot}}
}
func quoted(v string) string { return `"` + strings.ReplaceAll(v, `"`, `""`) + `"` }

// controlQuerier permits invariant checks inside the same transaction as fixture writes.
type controlQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// fingerprints stream deterministic row hashes for all actual Control tables, not a guessed table count.
func fingerprints(ctx context.Context, db controlQuerier, r registrationReceipt, exclude bool) (map[string]string, error) {
	rows, e := db.QueryContext(ctx, `SELECT name,sql FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if e != nil {
		return nil, e
	}
	type table struct{ name, ddl string }
	list := []table{}
	for rows.Next() {
		var x table
		if e = rows.Scan(&x.name, &x.ddl); e != nil {
			break
		}
		list = append(list, x)
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := map[string]string{}
	pred := recordPredicates(r)
	for _, x := range list {
		q := "SELECT * FROM " + quoted(x.name)
		args := []any{}
		if p, ok := pred[x.name]; exclude && ok {
			q += " WHERE " + quoted(p.field) + "<>?"
			args = append(args, p.value)
		}
		// Ordinal ordering is stable for the scalar/blob fields used by current Control tables.
		probe, e := db.QueryContext(ctx, q+" LIMIT 0", args...)
		if e != nil {
			return nil, e
		}
		cols, e := probe.Columns()
		probe.Close()
		if e != nil {
			return nil, e
		}
		order := []string{}
		for i := range cols {
			order = append(order, fmt.Sprint(i+1))
		}
		q += " ORDER BY " + strings.Join(order, ",")
		rs, e := db.QueryContext(ctx, q, args...)
		if e != nil {
			return nil, e
		}
		hash := sha256.New()
		hash.Write([]byte(x.ddl + "\n"))
		for rs.Next() {
			values := make([]any, len(cols))
			ptr := make([]any, len(cols))
			for i := range values {
				ptr[i] = &values[i]
			}
			if e = rs.Scan(ptr...); e != nil {
				break
			}
			raw, je := json.Marshal(values)
			if je != nil {
				e = je
				break
			}
			hash.Write(raw)
			hash.Write([]byte{'\n'})
		}
		if e == nil {
			e = rs.Err()
		}
		rs.Close()
		if e != nil {
			return nil, e
		}
		out[x.name] = hex.EncodeToString(hash.Sum(nil))
	}
	return out, nil
}

// registerFixture expects the already transferred private generation in its final package-owned directory.
// It never moves, rewrites or deletes generation files, and does not start the service.
func registerFixture(ctx context.Context, o registrationOptions) (r registrationReceipt, err error) {
	if !security.ValidID(o.Profile) || !security.ValidID(o.Job) || !security.ValidID(o.Snapshot) || len(o.SHA) != 64 || !filepath.IsAbs(o.Backup) {
		return r, errors.New("INVALID_REGISTRATION")
	}
	db, close, e := offlineControl(ctx, o.State)
	if e != nil {
		return r, e
	}
	defer close()
	r = registrationReceipt{State: o.State, Backup: o.Backup, Snapshot: o.Snapshot, Profile: o.Profile, Job: o.Job, GenerationSHA: o.SHA}
	root, binding, e := scanner.OpenBoundRoot(filepath.Join(o.State, "generations", o.Snapshot))
	if e != nil {
		return r, e
	}
	defer root.Close()
	// Private-file validation rejects symlinks, hardlinks and foreign ownership before full verification.
	file, e := security.OpenPrivateFile(filepath.Join(o.State, "generations", o.Snapshot, "index.db"), os.O_RDONLY)
	if e != nil {
		return r, e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return r, e
	}
	held, e := root.Lstat("index.db")
	if e != nil || !os.SameFile(info, held) {
		return r, errors.New("GENERATION_REPLACED")
	}
	sha, e := hashHeld(ctx, file)
	if e != nil || sha != o.SHA {
		return r, errors.New("GENERATION_SHA_MISMATCH")
	}
	gen, e := sql.Open("sqlite", readonlyURI(file))
	if e != nil {
		return r, e
	}
	defer gen.Close()
	gen.SetMaxOpenConns(1)
	h, e := index.ReadGenerationHeader(ctx, gen, o.Snapshot)
	if e != nil {
		return r, e
	}
	if h.Profile.ID != o.Profile || h.JobID != o.Job || h.Profile.Active || h.State != "complete" || h.Manifest.Provenance.Authority != "synthetic-fixture" {
		return r, errors.New("FIXTURE_BINDING_MISMATCH")
	}
	if e = index.VerifyGeneration(ctx, gen, o.Snapshot, o.Profile, o.Job); e != nil {
		return r, e
	}
	known := map[string]bool{}
	for _, x := range h.Manifest.Roots {
		known[x.TargetID] = true
	}
	if e = profiles.Validate(h.Profile, known); e != nil {
		return r, e
	}
	r.Stamp = fmt.Sprintf("%s:%d:%d", scanner.ObjectIdentity(info), info.Size(), info.ModTime().UnixNano())
	br, _ := json.Marshal(binding)
	r.Binding = string(br)
	// Refuse collisions across foreign references, including retired profile identities.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT count(*) FROM profiles WHERE id=?`, []any{o.Profile}}, {`SELECT count(*) FROM profile_tombstones WHERE id=?`, []any{o.Profile}},
		{`SELECT count(*) FROM jobs WHERE id=? OR json_extract(outcome_json,'$.job.profileId')=? OR json_extract(outcome_json,'$.job.resultSnapshotId')=?`, []any{o.Job, o.Profile, o.Snapshot}},
		{`SELECT count(*) FROM active_generations WHERE profile_id=? OR snapshot_id=?`, []any{o.Profile, o.Snapshot}},
		{`SELECT count(*) FROM profile_sources WHERE profile_id=?`, []any{o.Profile}},
		{`SELECT count(*) FROM integrity_generations WHERE snapshot_id=? OR profile_id=? OR job_id=?`, []any{o.Snapshot, o.Profile, o.Job}},
		{`SELECT count(*) FROM retention_qualification WHERE snapshot_id=? OR profile_id=? OR job_id=?`, []any{o.Snapshot, o.Profile, o.Job}},
	} {
		var n int
		if e = db.QueryRowContext(ctx, q.sql, q.args...).Scan(&n); e != nil {
			return r, e
		}
		if n != 0 {
			return r, errors.New("FIXTURE_ID_COLLISION")
		}
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT count(*) FROM integrity_operations WHERE snapshot_id=? OR profile_id=?`, []any{o.Snapshot, o.Profile}},
		{`SELECT count(*) FROM profile_cleanup WHERE snapshot_id=? OR profile_id=? OR job_id=?`, []any{o.Snapshot, o.Profile, o.Job}},
		{`SELECT count(*) FROM scan_queue WHERE profile_id=? OR job_id=?`, []any{o.Profile, o.Job}},
		{`SELECT (SELECT count(*) FROM job_controls WHERE job_id=?)+(SELECT count(*) FROM job_phases WHERE job_id=?)+(SELECT count(*) FROM job_progress WHERE job_id=?)`, []any{o.Job, o.Job, o.Job}},
	} {
		var n int
		if e = db.QueryRowContext(ctx, q.sql, q.args...).Scan(&n); e != nil {
			return r, e
		}
		if n != 0 {
			return r, errors.New("FIXTURE_ID_COLLISION")
		}
	}
	r.Baseline, e = fingerprints(ctx, db, r, false)
	if e != nil {
		return r, e
	}
	// VACUUM INTO takes a consistent SQLite backup even if the stopped database still has a WAL.
	backup, e := os.OpenFile(o.Backup, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return r, e
	}
	if e = backup.Close(); e != nil {
		return r, e
	}
	if _, e = db.ExecContext(ctx, "VACUUM INTO ?", o.Backup); e != nil {
		return r, e
	}
	backup, e = security.OpenPrivateFile(o.Backup, os.O_RDONLY)
	if e != nil {
		return r, e
	}
	defer backup.Close()
	saved, e := sql.Open("sqlite", readonlyURI(backup))
	if e != nil {
		return r, e
	}
	baseline, e := fingerprints(ctx, saved, r, false)
	saved.Close()
	if e != nil || !reflect.DeepEqual(baseline, r.Baseline) {
		return r, errors.New("BACKUP_MISMATCH")
	}
	r.BackupSHA, e = hashHeld(ctx, backup)
	if e != nil {
		return r, e
	}
	// The terminal outcome is explicitly fixture provenance, never an actual scan invocation.
	out := jobs.Outcome{SnapshotEndedAt: &h.EndedAt, FileTypeMapping: h.FileTypeMapping, ScopeManifest: &h.Manifest, SnapshotState: h.State, CoverageErrors: h.CoverageErrors, ServiceUID: os.Geteuid()}
	out.Job.ID = o.Job
	out.Job.ProfileID = o.Profile
	out.Job.ProfileRevision = h.Profile.Revision
	out.Job.CapturedProfile = h.Profile
	out.Job.Trigger = "cli"
	out.Job.ScanMode = "full"
	out.Job.State = "completed"
	out.Job.CreatedAt = h.StartedAt
	out.Job.StartedAt = &h.StartedAt
	out.Job.EndedAt = &h.EndedAt
	out.Job.ResultSnapshotID = &h.SnapshotID
	out.Job.EntriesVisited = fmt.Sprint(h.Entries)
	out.Job.BytesObserved = h.LogicalBytes
	out.Job.Warnings = []contracts.APIError{{Code: "SYNTHETIC_FIXTURE_IMPORT", MessageKey: "test.synthetic_fixture_import", RequestID: o.Job}}
	out.Job.ScannerVersion = h.ScannerVersion
	out.Job.SchemaVersion = h.SchemaVersion
	raw, _ := json.Marshal(out)
	r.OutcomeJSON = string(raw)
	praw, _ := json.Marshal(h.Profile)
	r.ProfileJSON = string(praw)
	// Rehash before taking the write transaction; original fixture bytes are never modified.
	postSHA, e := hashHeld(ctx, file)
	if e != nil || postSHA != o.SHA {
		return r, errors.New("GENERATION_CHANGED")
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	checkedAt := time.Now().UTC().Format(time.RFC3339Nano)
	if e = profiles.Import(ctx, tx, profiles.Record{Profile: h.Profile, RootsJSON: "{}", VolumesJSON: "{}"}, known); e != nil {
		return r, e
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO jobs(id,state,outcome_json) VALUES(?,'completed',?)`, []any{o.Job, r.OutcomeJSON}},
		{`INSERT INTO active_generations VALUES(?,?)`, []any{o.Profile, o.Snapshot}},
		{`INSERT INTO integrity_generations VALUES(?,?,?,'healthy',?,1,?,?)`, []any{o.Snapshot, o.Profile, o.Job, r.Stamp, r.Binding, checkedAt}},
		{`INSERT INTO retention_qualification(snapshot_id,profile_id,job_id,stamp,attempts) VALUES(?,?,?,?,0)`, []any{o.Snapshot, o.Profile, o.Job, r.Stamp}},
	} {
		if _, e = tx.ExecContext(ctx, q.sql, q.args...); e != nil {
			return r, e
		}
	}
	fresh, currentBinding, e := scanner.OpenBoundRoot(filepath.Join(o.State, "generations", o.Snapshot))
	if e != nil {
		return r, e
	}
	fresh.Close()
	if currentBinding != binding {
		return r, errors.New("GENERATION_DIRECTORY_CHANGED")
	}
	current, e := root.Lstat("index.db")
	if e != nil || !os.SameFile(info, current) || info.Size() != current.Size() || !info.ModTime().Equal(current.ModTime()) {
		return r, errors.New("GENERATION_CHANGED")
	}
	// Own-row trigger effects must not change the evidence excluded from foreign-row fingerprints.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT count(*) FROM profiles WHERE id=? AND revision=? AND profile_json=? AND roots_json='{}' AND volumes_json='{}'`, []any{o.Profile, h.Profile.Revision, r.ProfileJSON}},
		{`SELECT count(*) FROM jobs WHERE id=? AND state='completed' AND outcome_json=?`, []any{o.Job, r.OutcomeJSON}},
		{`SELECT count(*) FROM active_generations WHERE profile_id=? AND snapshot_id=?`, []any{o.Profile, o.Snapshot}},
		{`SELECT count(*) FROM integrity_generations WHERE snapshot_id=? AND profile_id=? AND job_id=? AND quality='healthy' AND stamp=? AND owned=1 AND binding=? AND checked_at=?`, []any{o.Snapshot, o.Profile, o.Job, r.Stamp, r.Binding, checkedAt}},
		{`SELECT count(*) FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=? AND stamp=? AND attempts=0`, []any{o.Snapshot, o.Profile, o.Job, r.Stamp}},
	} {
		var n int
		if e = tx.QueryRowContext(ctx, q.sql, q.args...).Scan(&n); e != nil || n != 1 {
			return r, errors.New("FIXTURE_RECORD_CHANGED")
		}
	}
	// Foreign trigger effects must abort before commit, not merely fail a later readback.
	invariant, e := fingerprints(ctx, tx, r, true)
	if e != nil || !reflect.DeepEqual(invariant, r.Baseline) {
		return r, errors.New("CONTROL_PRESERVATION_FAILED")
	}
	if e = tx.Commit(); e != nil {
		return r, e
	}
	after, e := fingerprints(ctx, db, r, true)
	if e != nil || !reflect.DeepEqual(after, r.Baseline) {
		return r, errors.New("CONTROL_PRESERVATION_FAILED")
	}
	return r, nil
}

// unregisterFixture removes only the exact inserted records; unrelated runtime changes require review.
// The generation file remains available for independent hashes and explicit external cleanup.
func unregisterFixture(ctx context.Context, r registrationReceipt) error {
	db, close, e := offlineControl(ctx, r.State)
	if e != nil {
		return e
	}
	defer close()
	// Reversal requires the original independently verified backup, not just caller-supplied IDs.
	backup, e := security.OpenPrivateFile(r.Backup, os.O_RDONLY)
	if e != nil {
		return e
	}
	defer backup.Close()
	backupSHA, e := hashHeld(ctx, backup)
	if e != nil || backupSHA != r.BackupSHA {
		return errors.New("BACKUP_CHANGED")
	}
	saved, e := sql.Open("sqlite", readonlyURI(backup))
	if e != nil {
		return e
	}
	baseline, e := fingerprints(ctx, saved, r, false)
	saved.Close()
	if e != nil || !reflect.DeepEqual(baseline, r.Baseline) {
		return errors.New("BACKUP_MISMATCH")
	}
	before, e := fingerprints(ctx, db, r, true)
	if e != nil || !reflect.DeepEqual(before, r.Baseline) {
		return errors.New("CONTROL_CHANGED_REVIEW_REQUIRED")
	}
	var raw, profile, snapshot string
	if e = db.QueryRowContext(ctx, "SELECT outcome_json FROM jobs WHERE id=?", r.Job).Scan(&raw); e != nil || raw != r.OutcomeJSON {
		return errors.New("FIXTURE_OUTCOME_CHANGED")
	}
	if e = db.QueryRowContext(ctx, "SELECT profile_json FROM profiles WHERE id=?", r.Profile).Scan(&profile); e != nil || profile != r.ProfileJSON {
		return errors.New("FIXTURE_PROFILE_CHANGED")
	}
	if e = db.QueryRowContext(ctx, "SELECT snapshot_id FROM active_generations WHERE profile_id=?", r.Profile).Scan(&snapshot); e != nil || snapshot != r.Snapshot {
		return errors.New("FIXTURE_POINTER_CHANGED")
	}
	// Healthy ownership and qualification must still describe the registered generation exactly.
	var qualified int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM integrity_generations WHERE snapshot_id=? AND profile_id=? AND job_id=? AND quality='healthy' AND stamp=? AND owned=1 AND binding=?`, r.Snapshot, r.Profile, r.Job, r.Stamp, r.Binding).Scan(&qualified); e != nil || qualified != 1 {
		return errors.New("FIXTURE_INTEGRITY_CHANGED")
	}
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM retention_qualification WHERE snapshot_id=? AND profile_id=? AND job_id=? AND stamp=?`, r.Snapshot, r.Profile, r.Job, r.Stamp).Scan(&qualified); e != nil || qualified != 1 {
		return errors.New("FIXTURE_QUALIFICATION_CHANGED")
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for table, p := range recordPredicates(r) {
		res, e := tx.ExecContext(ctx, "DELETE FROM "+quoted(table)+" WHERE "+quoted(p.field)+"=?", p.value)
		if e != nil {
			return e
		}
		n, e := res.RowsAffected()
		if e != nil || n != 1 {
			return errors.New("FIXTURE_RECORD_MISSING")
		}
	}
	// Deletion-trigger side effects also roll back with the five fixture records.
	invariant, e := fingerprints(ctx, tx, r, false)
	if e != nil || !reflect.DeepEqual(invariant, r.Baseline) {
		return errors.New("CONTROL_RESTORE_MISMATCH")
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	after, e := fingerprints(ctx, db, r, false)
	if e != nil || !reflect.DeepEqual(after, r.Baseline) {
		return errors.New("CONTROL_RESTORE_MISMATCH")
	}
	return nil
}

// TestRegisterNativeFixture is opt-in and registers only an already installed private synthetic generation.
func TestRegisterNativeFixture(t *testing.T) {
	if os.Getenv("DISKMAP_REGISTER_STATE") == "" {
		t.Skip("explicit offline test registration required")
	}
	// Reserve the receipt filename before mutation; even an ambiguous outcome is retained for review.
	path := os.Getenv("DISKMAP_REGISTER_RECEIPT")
	if !filepath.IsAbs(path) {
		t.Fatal("absolute receipt path required")
	}
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	r, e := registerFixture(ctx, registrationOptions{State: os.Getenv("DISKMAP_REGISTER_STATE"), Backup: os.Getenv("DISKMAP_REGISTER_BACKUP"), Snapshot: os.Getenv("DISKMAP_REGISTER_SNAPSHOT"), Profile: os.Getenv("DISKMAP_REGISTER_PROFILE"), Job: os.Getenv("DISKMAP_REGISTER_JOB"), SHA: os.Getenv("DISKMAP_REGISTER_SHA256")})
	b, je := json.Marshal(r)
	if je != nil {
		t.Fatal(je)
	}
	if _, we := file.Write(b); we != nil {
		t.Fatal(we)
	}
	if se := file.Sync(); se != nil {
		t.Fatal(se)
	}
	if e != nil {
		t.Fatal(e)
	}
	t.Log(string(b))
}

// readRegistrationReceipt accepts only a bounded, private, hash-qualified offline receipt.
func readRegistrationReceipt(path, expectedSHA string) (registrationReceipt, error) {
	var r registrationReceipt
	if !filepath.IsAbs(path) || len(expectedSHA) != 64 {
		return r, errors.New("INVALID_RECEIPT")
	}
	f, e := security.OpenPrivateFile(path, os.O_RDONLY)
	if e != nil {
		return r, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if e != nil || len(b) > 2<<20 {
		return r, errors.New("INVALID_RECEIPT")
	}
	h := sha256.Sum256(b)
	if hex.EncodeToString(h[:]) != expectedSHA {
		return r, errors.New("RECEIPT_SHA_MISMATCH")
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&r); e != nil {
		return r, e
	}
	var extra any
	if d.Decode(&extra) != io.EOF || !security.ValidID(r.Profile) || !security.ValidID(r.Job) || !security.ValidID(r.Snapshot) || len(r.BackupSHA) != 64 || len(r.GenerationSHA) != 64 || len(r.Baseline) == 0 || r.OutcomeJSON == "" || r.ProfileJSON == "" {
		return r, errors.New("INVALID_RECEIPT")
	}
	return r, nil
}

// TestUnregisterNativeFixture is an explicit offline reversal, never automatic teardown.
func TestUnregisterNativeFixture(t *testing.T) {
	path := os.Getenv("DISKMAP_UNREGISTER_RECEIPT")
	if path == "" {
		t.Skip("explicit offline receipt required")
	}
	r, e := readRegistrationReceipt(path, os.Getenv("DISKMAP_UNREGISTER_RECEIPT_SHA256"))
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()
	if e = unregisterFixture(ctx, r); e != nil {
		t.Fatal(e)
	}
	t.Log("fixture records removed; generation and backup preserved")
}
