package migration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Wrapped callback cancellation remains classifiable without publishing candidate mutations or raw text.
func TestApplyCancellationClassification(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			state := fixture(t)
			if err := Prepare(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(filepath.Join(state, "control.db"))
			if err != nil {
				t.Fatal(err)
			}
			err = Apply(context.Background(), state, func(ctx context.Context, db *sql.DB) error {
				if _, err := db.ExecContext(ctx, "DELETE FROM profiles"); err != nil {
					t.Fatal(err)
				}
				return fmt.Errorf("private/path secret: %w", cause)
			})
			if !errors.Is(err, cause) || !errors.Is(err, ErrUpgrade) || strings.Contains(err.Error(), "private") {
				t.Fatal("classification or redaction failed")
			}
			after, e := os.ReadFile(filepath.Join(state, "control.db"))
			if e != nil || !bytes.Equal(original, after) {
				t.Fatal("live control changed", e)
			}
			if e = Restore(context.Background(), state); e != nil {
				t.Fatal(e)
			}
		})
	}
	// Outer cancellation must also survive an otherwise generic callback error.
	state := fixture(t)
	if err := Prepare(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Apply(ctx, state, func(context.Context, *sql.DB) error { cancel(); return errors.New("private generic failure") })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUpgrade) || strings.Contains(err.Error(), "private") {
		t.Fatal("outer cancellation lost")
	}
}

// fixture creates a cold private configuration database without any source or generation files.
func fixture(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	os.Chmod(state, 0700)
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE profiles(id TEXT PRIMARY KEY,name TEXT); INSERT INTO profiles VALUES('profile','private-name'); PRAGMA user_version=2`)
	db.Close()
	os.Chmod(filepath.Join(state, "control.db"), 0600)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(state, "inventory.json"), []byte(`{"stateVersion":1}`), 0600)
	return state
}
func step(ctx context.Context, db *sql.DB) error {
	_, e := db.ExecContext(ctx, `CREATE TABLE schema_steps(version INTEGER PRIMARY KEY); INSERT INTO schema_steps VALUES(7); PRAGMA user_version=7`)
	return e
}

// Successful publication retains configuration and two exact recovery bundles across repeated upgrades.
func TestUpgradeSuccessAndRetention(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if e := Prepare(ctx, state); e != nil {
			t.Fatal(i, e)
		}
		apply := step
		if i > 0 {
			apply = func(context.Context, *sql.DB) error { return nil }
		}
		if e := Apply(ctx, state, apply); e != nil {
			t.Fatal(i, e)
		}
		if e := Apply(ctx, state, apply); e != nil {
			t.Fatal("apply replay", e)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(state, "upgrades"))
	if len(entries) != 2 {
		t.Fatal(len(entries))
	}
	db, _ := sql.Open("sqlite", filepath.Join(state, "control.db"))
	defer db.Close()
	var name string
	if db.QueryRow("SELECT name FROM profiles WHERE id='profile'").Scan(&name) != nil || name != "private-name" {
		t.Fatal(name)
	}
}

// An intentionally failed candidate leaves original bytes untouched; explicit restore is separately audited.
func TestFailedMigrationPreservesAndRestores(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	original, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	err := Apply(ctx, state, func(ctx context.Context, db *sql.DB) error {
		db.Exec("DELETE FROM profiles")
		return errors.New("private/path")
	})
	if err != ErrUpgrade {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !bytes.Equal(original, after) {
		t.Fatal("live database changed")
	}
	if e := Restore(ctx, state); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(filepath.Join(state, "control.db"))
	if !bytes.Equal(original, after) {
		t.Fatal("restore differs")
	}
}

// Unsupported versions, aliases, hardlinks and live journal state cannot be backed up as consistent cold data.
func TestUpgradeRejectsUnsafeOrUnknown(t *testing.T) {
	for _, kind := range []string{"newer", "corrupt", "symlink", "hardlink", "journal"} {
		t.Run(kind, func(t *testing.T) {
			state := fixture(t)
			file := filepath.Join(state, "control.db")
			switch kind {
			case "newer":
				db, _ := sql.Open("sqlite", file)
				db.Exec("PRAGMA user_version=99")
				db.Close()
			case "corrupt":
				os.WriteFile(file, []byte("invalid"), 0600)
			case "symlink":
				os.Rename(file, file+".outside")
				os.Symlink(file+".outside", file)
			case "hardlink":
				os.Link(file, file+".other")
			case "journal":
				os.WriteFile(file+"-journal", nil, 0600)
			}
			if e := Prepare(context.Background(), state); e == nil {
				t.Fatal("unsafe accepted")
			}
			if _, e := os.Stat(filepath.Join(state, "upgrade-current.json")); !os.IsNotExist(e) {
				t.Fatal("published unsafe backup")
			}
		})
	}
}

// Startup is blocked while a prepared or restored operation requires explicit recovery.
func TestUpgradeMarkerAndAliasBoundary(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	if CheckReady(state) != nil {
		t.Fatal("fresh marker")
	}
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != ErrUpgrade {
		t.Fatal("pending startup accepted")
	}
	if e := Apply(ctx, state, step); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != nil {
		t.Fatal("applied startup blocked")
	}
	if e := Restore(ctx, state); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != ErrUpgrade {
		t.Fatal("restored startup accepted")
	}
	aliasState := fixture(t)
	outside := t.TempDir()
	os.Chmod(outside, 0700)
	if e := os.Symlink(outside, filepath.Join(aliasState, "upgrades")); e != nil {
		t.Fatal(e)
	}
	if e := Prepare(ctx, aliasState); e != ErrUpgrade {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("alias target mutated")
	}
}

// A crash after atomic database publication can only be confirmed by the exact candidate digest.
func TestPublicationRecoveryDoesNotRepeatMigration(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, state, step); e != nil {
		t.Fatal(e)
	}
	w, e := open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	m, base, e := w.current()
	if e != nil {
		t.Fatal(e)
	}
	m.Stage = "publishing"
	if e = w.write(base+"/manifest.json", m); e != nil {
		t.Fatal(e)
	}
	w.close()
	if CheckReady(state) != ErrUpgrade {
		t.Fatal("ambiguous publication accepted")
	}
	if e = Apply(ctx, state, func(context.Context, *sql.DB) error { t.Fatal("migration repeated"); return nil }); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != nil {
		t.Fatal("confirmed publication unavailable")
	}
}

// A valid SQLite backup altered after preparation cannot replace the unchanged live database.
func TestApplyRejectsChangedRecoveryBackup(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	before, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	w, e := open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	_, base, e := w.current()
	w.close()
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(state, base, "control.before.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DELETE FROM profiles`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	called := false
	if e = Apply(ctx, state, func(ctx context.Context, db *sql.DB) error { called = true; return step(ctx, db) }); e != ErrUpgrade || called {
		t.Fatal(e, called)
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !bytes.Equal(before, after) {
		t.Fatal("live data replaced from altered backup")
	}
}

// Inert temporary bytes left by a crash never block explicit restore or a later upgrade.
func TestRecoverySurvivesAbandonedManifestWrite(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	w, e := open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	_, base, e := w.current()
	w.close()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{base + "/manifest.json.tmp", base + "/manifest.json.tmp-0123456789abcdef0123456789abcdef", "upgrade-current.json.tmp"} {
		if e = os.WriteFile(filepath.Join(state, name), []byte("incomplete"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e = Restore(ctx, state); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != ErrUpgrade {
		t.Fatal("restored data wrongly starts")
	}
	if e = Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	if e = Apply(ctx, state, step); e != nil {
		t.Fatal(e)
	}
	if CheckReady(state) != nil {
		t.Fatal("recovered upgrade unavailable")
	}
	// Once obsolete, the manifest-bound bundle's strictly named private leftovers may be retired.
	if e = Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	if e = Apply(ctx, state, func(context.Context, *sql.DB) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(state, base)); !os.IsNotExist(e) {
		t.Fatal("obsolete qualified bundle not retired", e)
	}
}

// A completed prior-v3 bundle is history, not an instruction to apply or restore with a v4 binary.
func TestUpgradeFromCompletedVersion3Bundle(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	// Reconstruct the prior binary's applied v3 bytes and corresponding terminal manifest.
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_steps(version INTEGER PRIMARY KEY); INSERT INTO schema_steps VALUES(3); PRAGMA user_version=3"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	w, e := open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	m, base, e := w.current()
	if e != nil {
		t.Fatal(e)
	}
	f, e := w.file("control.db", os.O_RDONLY)
	if e != nil {
		t.Fatal(e)
	}
	m.CandidateSHA, e = digest(ctx, f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	m.TargetVersion = 3
	m.Stage = "applied"
	if e = w.write(base+"/manifest.json", m); e != nil {
		t.Fatal(e)
	}
	w.close()
	if e := Apply(ctx, state, step); e != ErrVersion {
		t.Fatal(e)
	}
	if e := Restore(ctx, state); e != ErrVersion {
		t.Fatal(e)
	}
	if e := Prepare(ctx, state); e != nil {
		t.Fatal("new v4 preparation blocked by completed historical bundle", e)
	}
}

// A completed schema-6 receipt remains history, never an instruction to reapply or downgrade.
func TestUpgradeFromCompletedVersion6Bundle(t *testing.T) {
	state := fixture(t)
	ctx := context.Background()
	if e := Prepare(ctx, state); e != nil {
		t.Fatal(e)
	}
	// Reconstruct the prior binary's applied v6 bytes and corresponding terminal manifest.
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_steps(version INTEGER PRIMARY KEY); INSERT INTO schema_steps VALUES(6); PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	w, e := open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	m, base, e := w.current()
	if e != nil {
		t.Fatal(e)
	}
	f, e := w.file("control.db", os.O_RDONLY)
	if e != nil {
		t.Fatal(e)
	}
	m.CandidateSHA, e = digest(ctx, f)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	m.TargetVersion = 6
	m.Stage = "applied"
	if e = w.write(base+"/manifest.json", m); e != nil {
		t.Fatal(e)
	}
	w.close()
	if e := Apply(ctx, state, step); e != ErrVersion {
		t.Fatal(e)
	}
	if e := Restore(ctx, state); e != ErrVersion {
		t.Fatal(e)
	}
	if e := Prepare(ctx, state); e != nil {
		t.Fatal("new v7 preparation blocked by completed historical bundle", e)
	}
}
