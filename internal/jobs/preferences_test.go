package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func prefAppearance(rev int) PreferenceRequest {
	return PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: rev, Group: "appearance", Appearance: &contracts.PreferenceAppearance{Language: "de", Theme: "dark", Map: "radial"}}
}

// Principals isolate rows and replay namespaces; defaults never write personal data.
func TestPreferencesIsolationCASReplayRestart(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	for _, p := range []string{"admin-a", "admin-b"} {
		v, e := m.Preferences(ctx, p)
		if e != nil || v.Revision != 0 || v.DSMUserID != p {
			t.Fatal(v, e)
		}
	}
	var n int
	m.db.QueryRow("SELECT count(*) FROM user_preferences").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	a, e := m.SetPreferences(ctx, prefAppearance(0), "admin-a", "key")
	if e != nil || a.Revision != 1 {
		t.Fatal(a, e)
	}
	b, e := m.SetPreferences(ctx, prefAppearance(0), "admin-b", "key")
	if e != nil || b.Revision != 1 {
		t.Fatal(b, e)
	}
	preferred := PreferenceRequest{ContractVersion: contracts.PreferenceVersion, ExpectedRevision: 1, Group: "preferredProfile", PreferredProfile: &PreferredProfileChange{ProfileID: &in.Profile.ID}}
	if _, e = m.SetPreferences(ctx, preferred, "admin-a", "next"); e != nil {
		t.Fatal(e)
	}
	replay, e := m.SetPreferences(ctx, prefAppearance(0), "admin-a", "key")
	if e != nil || !reflect.DeepEqual(replay, a) {
		t.Fatal(replay, e)
	}
	changed := prefAppearance(0)
	changed.Appearance.Language = "en"
	if _, e = m.SetPreferences(ctx, changed, "admin-a", "key"); e != ErrIdempotencyConflict {
		t.Fatal(e)
	}
	if _, e = m.SetPreferences(ctx, prefAppearance(0), "admin-a", "stale"); e != ErrPreferenceConflict {
		t.Fatal(e)
	}
	current, e := m.Preferences(ctx, "admin-a")
	if e != nil || current.Language != "de" || current.Revision != 2 || current.PreferredProfileID == nil {
		t.Fatal(current, e)
	}
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, e := m.SetPreferences(ctx, prefAppearance(2), "admin-a", key)
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				success++
			} else if e != ErrPreferenceConflict {
				t.Error(e)
			}
		}([]string{"race-a", "race-b"}[i])
	}
	wg.Wait()
	if success != 1 {
		t.Fatal(success)
	}
	state := m.state
	m.Close()
	next, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	v, e := next.Preferences(ctx, "admin-a")
	if e != nil || v.Revision != 3 || v.PreferredProfileID == nil {
		t.Fatal(v, e)
	}
}

// Invalid updates cannot burn replay keys, erase another group, or accept client filesystem references.
func TestPreferencesValidationAndRollback(t *testing.T) {
	m, in, _ := fixture(t)
	defer m.Close()
	m.ImportProfile(in)
	ctx := context.Background()
	for _, p := range []string{"", "a\nb", " admin"} {
		if _, e := m.Preferences(ctx, p); e != ErrInvalid {
			t.Fatal(e)
		}
	}
	bad := prefAppearance(0)
	bad.ExpectedRevision = MaxPreferenceRevision
	if bad.Validate() != ErrInvalid {
		t.Fatal("revision")
	}
	bad = prefAppearance(0)
	bad.Appearance.Theme = "unsafe"
	if bad.Validate() != ErrInvalid {
		t.Fatal("enum")
	}
	v := contracts.PreferenceView{ProfileID: in.Profile.ID, SnapshotID: "snapshot", RootID: "nas", Map: "treemap", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	request := PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "lastView", View: &PreferenceViewChange{Value: &v}}
	if _, e := m.SetPreferences(ctx, request, "admin", "view"); e != nil {
		t.Fatal(e)
	}
	got, e := m.SetPreferences(ctx, prefAppearance(1), "admin", "appearance")
	if e != nil || got.LastView == nil || got.LastView.RootID != "nas" {
		t.Fatal(got, e)
	}
	v.RootID = "/private/path"
	if request.Validate() != ErrInvalid {
		t.Fatal("path accepted")
	}
	v.RootID = "nas"
	v.Search.Scope = "outside"
	if request.Validate() != ErrInvalid {
		t.Fatal("scope")
	}
	m.db.Exec(`CREATE TRIGGER preference_replay_failure BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT,'test'); END`)
	if _, e = m.SetPreferences(ctx, prefAppearance(2), "admin", "rollback"); e == nil {
		t.Fatal("expected rollback")
	}
	current, e := m.Preferences(ctx, "admin")
	if e != nil || current.Revision != 2 {
		t.Fatal(current, e)
	}
}

// A v3 candidate gains only the explicit preference table/step; normal startup refuses unapproved mutation.
func TestPreferencesControlV3UpgradePreservesTables(t *testing.T) {
	m, in, _ := fixture(t)
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	db.Exec("DROP TABLE onboarding_drafts; DROP TABLE exclusion_template; DROP TABLE user_preferences; DELETE FROM schema_steps WHERE version IN (4,5,6,7); PRAGMA user_version=3")
	before := map[string]string{}
	rows, e := db.Query("SELECT name,sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var n, s string
		rows.Scan(&n, &s)
		before[n] = s
	}
	rows.Close()
	payloads := map[string]string{}
	for name := range before {
		payloads[name] = preferenceTableDump(t, db, name)
	}
	db.Close()
	original, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if opened, e := Open(context.Background(), state); !errors.Is(e, migration.ErrVersion) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal(e)
	}
	same, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !reflect.DeepEqual(original, same) {
		t.Fatal("normal startup mutated v3")
	}
	for _, mode := range []string{"prepare", "apply"} {
		if e := Upgrade(context.Background(), state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	db, _ = sql.Open("sqlite", filepath.Join(state, "control.db"))
	defer db.Close()
	for name, want := range before {
		var got string
		if e := db.QueryRow("SELECT sql FROM sqlite_master WHERE name=?", name).Scan(&got); e != nil || got != want {
			t.Fatal(name, e)
		}
		if got := preferenceTableDump(t, db, name); got != payloads[name] {
			t.Fatal("preexisting table rows changed", name)
		}
	}
	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != migration.ControlVersion {
		t.Fatal(version)
	}
}

// Decoding malformed persisted data fails closed instead of silently returning defaults.
func TestPreferencesCorruptStoredRow(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	v := defaultPreferences("admin")
	v.Revision = 1
	v.Language = "unsafe"
	raw, _ := json.Marshal(v)
	m.db.Exec("INSERT INTO user_preferences VALUES(?,?,?)", "admin", 1, string(raw))
	if _, e := m.Preferences(context.Background(), "admin"); e != ErrUnavailable {
		t.Fatal(e)
	}
}

// The candidate transaction rolls back on a deliberately conflicting migration marker.
func TestPreferencesMigrationFailureAndRestore(t *testing.T) {
	m, _, _ := fixture(t)
	state := m.state
	m.Close()
	db, _ := sql.Open("sqlite", filepath.Join(state, "control.db"))
	db.Exec("DROP TABLE user_preferences; PRAGMA user_version=3")
	db.Close()
	before, _ := os.ReadFile(filepath.Join(state, "control.db"))
	ctx := context.Background()
	if e := Upgrade(ctx, state, "prepare"); e != nil {
		t.Fatal(e)
	}
	if e := Upgrade(ctx, state, "apply"); e == nil {
		t.Fatal("conflicting migration step accepted")
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed migration changed original")
	}
	if e := Upgrade(ctx, state, "restore"); e != nil {
		t.Fatal(e)
	}
	after, _ = os.ReadFile(filepath.Join(state, "control.db"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("restore changed source")
	}
}

// Query selectors retain their production meanings; invalid identities and size limits fail before persistence.
func TestPreferencesSelectorLimits(t *testing.T) {
	base := contracts.PreferenceView{ProfileID: "profile", SnapshotID: "snapshot", RootID: "nas", Map: "treemap", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	for _, mutate := range []func(*contracts.PreferenceView){
		func(v *contracts.PreferenceView) { v.ProfileID = "../profile" }, func(v *contracts.PreferenceView) { v.SnapshotID = "/snapshot" },
		func(v *contracts.PreferenceView) { v.Metric = "unknown" }, func(v *contracts.PreferenceView) { v.FilterMode = "exclude" },
		func(v *contracts.PreferenceView) { v.Sort = "path" }, func(v *contracts.PreferenceView) { v.Direction = "down" },
		func(v *contracts.PreferenceView) { v.Filter.Categories = []string{"secret"} }, func(v *contracts.PreferenceView) { v.Filter.ObjectKinds = []string{"fifo"} },
		func(v *contracts.PreferenceView) { v.Search.Text = string(make([]byte, 513)) },
		func(v *contracts.PreferenceView) { bad := contracts.Bytes("01"); v.Filter.MinSize = &bad },
	} {
		v := base
		mutate(&v)
		if validatePreferenceView(v) != ErrInvalid {
			t.Fatal("invalid selector accepted", v)
		}
	}
	if e := validatePreferenceView(base); e != nil {
		t.Fatal(e)
	}
	m, _, _ := fixture(t)
	defer m.Close()
	id := "missing"
	in := PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "preferredProfile", PreferredProfile: &PreferredProfileChange{ProfileID: &id}}
	if _, e := m.SetPreferences(context.Background(), in, "admin", "invalid-reference"); e != ErrInvalid {
		t.Fatal(e)
	}
	var count int
	m.db.QueryRow("SELECT count(*) FROM idempotency WHERE operation='preferences.update'").Scan(&count)
	if count != 0 {
		t.Fatal("invalid request burned replay key")
	}
}

// This bounded fixture serializes all preexisting control rows, including replay/queue/settings state.
func preferenceTableDump(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	statement := `SELECT * FROM "` + name + `"`
	if name == "schema_steps" {
		statement += " WHERE version<=3"
	}
	rows, e := db.Query(statement + " ORDER BY rowid")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		t.Fatal(e)
	}
	var all [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range values {
			ptr[i] = &values[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			t.Fatal(e)
		}
		all = append(all, values)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(all)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}

// Saturated stored revisions remain readable and old receipts remain replayable without wraparound.
func TestPreferencesRevisionSaturationAndRestartReplay(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	accepted, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "original")
	if e != nil {
		t.Fatal(e)
	}
	saturated := accepted
	saturated.Revision = MaxPreferenceRevision
	raw, _ := json.Marshal(saturated)
	if _, e = m.db.Exec("UPDATE user_preferences SET revision=?,value_json=? WHERE principal=?", saturated.Revision, string(raw), "admin"); e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	m, e = Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	current, e := m.Preferences(ctx, "admin")
	if e != nil || current.Revision != MaxPreferenceRevision {
		t.Fatal(current, e)
	}
	replay, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "original")
	if e != nil || !reflect.DeepEqual(replay, accepted) {
		t.Fatal(replay, e)
	}
	if _, e = m.SetPreferences(ctx, prefAppearance(MaxPreferenceRevision), "admin", "new"); e != ErrInvalid {
		t.Fatal(e)
	}
	current, e = m.Preferences(ctx, "admin")
	if e != nil || current.Revision != MaxPreferenceRevision {
		t.Fatal(current, e)
	}
}

// Stored replay receipts are not trusted merely because SQLite contains syntactically valid JSON.
func TestPreferencesReplayReceiptQualification(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	in := prefAppearance(0)
	accepted, e := m.SetPreferences(ctx, in, "admin", "key")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(accepted)
	cases := []string{
		strings.Replace(string(raw), `"dsmUserId":"admin"`, `"dsmUserId":"foreign"`, 1),
		strings.Replace(string(raw), `"revision":1`, `"revision":-1`, 1),
		strings.Replace(string(raw), `"revision":1`, `"revision":0`, 1),
		strings.Replace(string(raw), `"lastView":null`, `"lastView":{"privateField":"hidden"}`, 1),
	}
	for _, bad := range cases {
		if _, e = m.db.Exec("UPDATE idempotency SET result_json=? WHERE principal=? AND operation=? AND key=?", bad, "admin", "preferences.update", "key"); e != nil {
			t.Fatal(e)
		}
		if _, e = m.SetPreferences(ctx, in, "admin", "key"); e != ErrUnavailable {
			t.Fatal(e)
		}
		current, e := m.Preferences(ctx, "admin")
		if e != nil || !reflect.DeepEqual(current, accepted) {
			t.Fatal(current, e)
		}
	}
}

// Missing persisted owner/version/enums cannot inherit defaults and masquerade as a valid row.
func TestPreferencesSparseStoredRowFailsClosed(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	if _, e := m.db.Exec("INSERT INTO user_preferences VALUES(?,?,?)", "admin", 1, `{"revision":1}`); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Preferences(context.Background(), "admin"); e != ErrUnavailable {
		t.Fatal(e)
	}
	if _, e := m.SetPreferences(context.Background(), prefAppearance(1), "admin", "attempt"); e != ErrUnavailable {
		t.Fatal(e)
	}
	var raw string
	m.db.QueryRow("SELECT value_json FROM user_preferences WHERE principal='admin'").Scan(&raw)
	if raw != `{"revision":1}` {
		t.Fatal("corrupt row was silently repaired")
	}
}

// Recovery preserves existing personal values as well as the original control bytes.
func TestPreferencesRecoveryPreservesPersonalValues(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	expected, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "key")
	if e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	before, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if e = Upgrade(ctx, state, "prepare"); e != nil {
		t.Fatal(e)
	}
	e = migration.Apply(ctx, state, func(ctx context.Context, db *sql.DB) error {
		if _, err := db.ExecContext(ctx, "DELETE FROM user_preferences"); err != nil {
			return err
		}
		return errors.New("injected candidate failure")
	})
	if e == nil {
		t.Fatal("expected failed apply")
	}
	if e = Upgrade(ctx, state, "restore"); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(filepath.Join(state, "control.db"))
	if !reflect.DeepEqual(before, after) {
		t.Fatal("recovery changed personal database bytes")
	}
	// Explicit restore intentionally blocks normal startup until another qualified apply.
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	actual, e := readPreferences(ctx, db, "admin")
	if e != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal(actual, e)
	}
}

// Workspace writes survive reopening and cannot change another administrator's presentation state.
func TestWorkspacePreferencesRoundTripAndIsolation(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	workspace := contracts.PreferenceWorkspace{Left: true, Right: true, LeftWidth: 320, RightWidth: 410, Expanded: []string{"source:shares:s:."}, TreeSort: "size", TreeDirection: "desc", TreeScroll: 72, ListScroll: 108, FilterOpen: true, List: contracts.WorkspaceList{Text: "*.zip", Recursive: true, Kind: "file", Category: "archive", Sort: "name", Direction: "asc", FoldersFirst: true}, ShowSystem: true}
	// The shared size unit survives reopening with the rest of the principal's workspace.
	workspace.SizeUnit = "mixed"
	// A disabled share status must remain distinct from an older workspace without this field.
	showShareStatus := false
	workspace.ShowShareStatus = &showShareStatus
	req := PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "workspace", Workspace: &workspace}
	out, err := m.SetPreferences(ctx, req, "admin-a", "workspace-one")
	if err != nil || !reflect.DeepEqual(out.Workspace, &workspace) {
		t.Fatal(out, err)
	}
	other, err := m.Preferences(ctx, "admin-b")
	if err != nil || other.Workspace != nil {
		t.Fatal(other, err)
	}
	replay, err := m.SetPreferences(ctx, req, "admin-a", "workspace-one")
	if err != nil || !reflect.DeepEqual(out, replay) {
		t.Fatal(replay, err)
	}
	state := m.state
	m.Close()
	next, err := Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	restored, err := next.Preferences(ctx, "admin-a")
	if err != nil || !reflect.DeepEqual(restored.Workspace, &workspace) {
		t.Fatal(restored, err)
	}
	workspace.LeftWidth = 99999
	req.Workspace = &workspace
	if req.Validate() == nil {
		t.Fatal("unbounded workspace accepted")
	}
}
