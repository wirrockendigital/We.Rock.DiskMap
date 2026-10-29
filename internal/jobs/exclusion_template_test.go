package jobs

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// A shared template uses CAS while receipts remain isolated by principal and immutable after reset.
func TestExclusionTemplateCASReplayReset(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	v, e := m.ExclusionTemplate(ctx, "a")
	if e != nil || v.Revision != 0 || len(v.Rules) != 0 {
		t.Fatal(v, e)
	}
	in := ExclusionTemplateRequest{1, 0, []contracts.Exclusion{{Kind: "pattern", Value: "**/*.tmp"}}}
	v, e = m.SetExclusionTemplate(ctx, in, "a", "one")
	if e != nil || v.Revision != 1 {
		t.Fatal(v, e)
	}
	if _, e = m.SetExclusionTemplate(ctx, in, "b", "one"); !errors.Is(e, ErrTemplateConflict) {
		t.Fatal(e)
	}
	reset := ExclusionTemplateRequest{1, 1, []contracts.Exclusion{}}
	if _, e = m.SetExclusionTemplate(ctx, reset, "b", "reset"); e != nil {
		t.Fatal(e)
	}
	v, e = m.SetExclusionTemplate(ctx, in, "a", "one")
	if e != nil || v.Revision != 1 {
		t.Fatal(v, e)
	}
	v, e = m.ExclusionTemplate(ctx, "a")
	if e != nil || v.Revision != 2 || len(v.Rules) != 0 {
		t.Fatal(v, e)
	}
	in.Rules[0].Value = "*.bak"
	if _, e = m.SetExclusionTemplate(ctx, in, "a", "one"); e != ErrIdempotencyConflict {
		t.Fatal(e)
	}
}

// Malformed rows and receipts never become empty defaults or trusted successful writes.
func TestExclusionTemplateCorruptionAndRollback(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	in := ExclusionTemplateRequest{1, 0, []contracts.Exclusion{}}
	m.db.Exec(`CREATE TRIGGER template_fail BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'test'); END`)
	if _, e := m.SetExclusionTemplate(ctx, in, "admin", "key"); e == nil {
		t.Fatal("rollback missing")
	}
	v, e := m.ExclusionTemplate(ctx, "admin")
	if e != nil || v.Revision != 0 {
		t.Fatal(v, e)
	}
	m.db.Exec(`DROP TRIGGER template_fail`)
	if _, e = m.SetExclusionTemplate(ctx, in, "admin", "key"); e != nil {
		t.Fatal(e)
	}
	m.db.Exec(`UPDATE idempotency SET result_json='{"version":1,"revision":1,"rules":[],"extra":true}'`)
	if _, e = m.SetExclusionTemplate(ctx, in, "admin", "key"); e != ErrUnavailable {
		t.Fatal(e)
	}
	m.db.Exec(`UPDATE exclusion_template SET value_json='{"revision":1}'`)
	if _, e = m.ExclusionTemplate(ctx, "admin"); e != ErrUnavailable {
		t.Fatal(e)
	}
	m.db.Exec(`DELETE FROM exclusion_template`)
	if _, e = m.ExclusionTemplate(ctx, "admin"); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// Canonical JSON escaping is included in the action byte budget, not only raw rule text.
func TestExclusionTemplateBounds(t *testing.T) {
	for _, rules := range [][]contracts.Exclusion{nil, {{Kind: "path", Value: "../outside"}}, {{Kind: "pattern", Value: strings.Repeat("&", 3000)}}} {
		if (ExclusionTemplateRequest{1, 0, rules}).Validate() != ErrInvalid {
			t.Fatal("accepted invalid rules")
		}
	}
	if (ExclusionTemplateRequest{1, 0, []contracts.Exclusion{{Kind: "pattern", Value: "Grüße😀.*"}}}).Validate() != nil {
		t.Fatal("unicode")
	}
}

// A real v4 offline candidate preserves all existing table rows while adding the singleton.
func TestExclusionTemplateMigrationV4(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	if _, e := m.SetPreferences(context.Background(), prefAppearance(0), "admin", "pref"); e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	downgradePreferenceFixture(t, db)
	if _, e = db.Exec("DROP TABLE onboarding_drafts; DELETE FROM schema_steps WHERE version IN (6,7); DROP TABLE exclusion_template; DELETE FROM schema_steps WHERE version=5; PRAGMA user_version=4"); e != nil {
		t.Fatal(e)
	}
	names := []string{}
	rows, e := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_steps'")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	before := map[string]string{}
	for _, n := range names {
		before[n] = preferenceTableDump(t, db, n)
	}
	db.Close()
	if x, e := Open(context.Background(), state); e != migration.ErrVersion {
		if x != nil {
			x.Close()
		}
		t.Fatal(e)
	}
	for _, mode := range []string{"prepare", "apply"} {
		if e := Upgrade(context.Background(), state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	next, e := Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	for n, want := range before {
		if n == "user_preferences" || n == "idempotency" {
			continue
		} // Preference v2 has a separate explicit migration oracle.
		if got := preferenceTableDump(t, next.db, n); got != want {
			t.Fatal("changed table", n)
		}
	}
	if got, e := next.Preferences(context.Background(), "admin"); e != nil || got.Language != "de" || got.Revision != 1 {
		t.Fatal(got, e)
	}
	v, e := next.ExclusionTemplate(context.Background(), "admin")
	if e != nil || v.Revision != 0 {
		t.Fatal(v, e)
	}
}

// A failed migration step rolls back both the new table and schema version.
func TestExclusionTemplateMigrationRollback(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	if _, e := m.db.Exec("DROP TABLE exclusion_template; PRAGMA user_version=4"); e != nil {
		t.Fatal(e)
	}
	if e := migrateExclusionTemplate(context.Background(), m.db); e == nil {
		t.Fatal("existing step must conflict")
	}
	var n, v int
	m.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='exclusion_template'").Scan(&n)
	m.db.QueryRow("PRAGMA user_version").Scan(&v)
	if n != 0 || v != 4 {
		t.Fatal(n, v)
	}
}

// Concurrent administrators share one CAS domain; restart preserves both value and old receipts.
func TestExclusionTemplateConcurrentRestart(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	// Seed the personal row explicitly before proving that global CAS and restart preserve it.
	if got, e := m.SetPreferences(context.Background(), prefAppearance(0), "admin", "personal-baseline"); e != nil || got.Language != "de" || got.Revision != 1 {
		t.Fatal(got, e)
	}
	personalBefore := preferenceTableDump(t, m.db, "user_preferences")
	before := preferenceTableDump(t, m.db, "profiles")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	request := ExclusionTemplateRequest{1, 0, []contracts.Exclusion{{Kind: "path", Value: "cache"}}}
	for _, p := range []string{"a", "b"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_, e := m.SetExclusionTemplate(context.Background(), request, p, "same")
			results <- e
		}(p)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if e != ErrTemplateConflict {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	if preferenceTableDump(t, m.db, "profiles") != before {
		t.Fatal("profile changed")
	}
	state := m.state
	m.Close()
	next, e := Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	if got, e := next.Preferences(context.Background(), "admin"); e != nil || got.Language != "de" || got.Revision != 1 {
		t.Fatal(got, e)
	}
	if preferenceTableDump(t, next.db, "user_preferences") != personalBefore {
		t.Fatal("global template changed personal preferences")
	}
	v, e := next.ExclusionTemplate(context.Background(), "admin")
	if e != nil || v.Revision != 1 || v.Rules[0].Value != "cache" {
		t.Fatal(v, e)
	}
	for _, p := range []string{"a", "b"} {
		v, e = next.SetExclusionTemplate(context.Background(), request, p, "same")
		if e == nil && v.Revision != 1 {
			t.Fatal(v)
		}
		if e != nil && e != ErrTemplateConflict {
			t.Fatal(e)
		}
	}
}
