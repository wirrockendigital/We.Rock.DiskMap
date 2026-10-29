package jobs

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
)

// Offline v5 upgrades preserve existing domain rows; runtime startup never silently migrates them.
func TestOnboardingMigrationV5(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	if _, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "pref"); e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	downgradePreferenceFixture(t, db)
	if _, e = db.Exec("DROP TABLE onboarding_drafts;DELETE FROM schema_steps WHERE version IN (6,7);PRAGMA user_version=5"); e != nil {
		t.Fatal(e)
	}
	before := map[string]string{}
	rows, e := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_steps'")
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		before[n] = preferenceTableDump(t, db, n)
	}
	db.Close()
	if x, e := Open(ctx, state); e != migration.ErrVersion {
		if x != nil {
			x.Close()
		}
		t.Fatal(e)
	}
	for _, mode := range []string{"prepare", "apply"} {
		if e := Upgrade(ctx, state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	next, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	for n, want := range before {
		if n == "user_preferences" || n == "idempotency" {
			continue
		} // Preference v2 has a separate explicit migration oracle.
		if got := preferenceTableDump(t, next.db, n); got != want {
			t.Fatal("mutated", n)
		}
	}
	if got, e := next.Preferences(ctx, "admin"); e != nil || got.Language != "de" || got.Revision != 1 {
		t.Fatal(got, e)
	}
	out, e := next.OnboardingDraft(ctx, "admin")
	if e != nil || out.Revision != 0 || out.Draft != nil {
		t.Fatal(out, e)
	}
}

// A conflicting schema receipt rolls back table creation and leaves the original version untouched.
func TestOnboardingMigrationRollback(t *testing.T) {
	m, _, _ := fixture(t)
	if _, e := m.db.Exec("DROP TABLE onboarding_drafts;PRAGMA user_version=5"); e != nil {
		t.Fatal(e)
	}
	if e := migrateOnboarding(context.Background(), m.db); e == nil {
		t.Fatal("conflicting step accepted")
	}
	var n, v int
	m.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='onboarding_drafts'").Scan(&n)
	m.db.QueryRow("PRAGMA user_version").Scan(&v)
	if n != 0 || v != 5 {
		t.Fatal(n, v)
	}
}
