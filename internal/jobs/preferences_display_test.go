package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// Historical fixtures contain actual v1 payloads/DTOs, not current rows with a false schema label.
func downgradePreferenceFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`UPDATE user_preferences SET value_json=json_remove(json_set(value_json,'$.version',1),'$.display')`,
		`UPDATE idempotency SET result_json=json_remove(json_set(result_json,'$.version',1),'$.display'),payload=json_set(payload,'$.contractVersion',1) WHERE operation='preferences.update'`,
	} {
		if _, e := db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
}

// Every required field is checked through the same bounded decoder as service/CGI.
func TestPreferenceDisplayStrictWire(t *testing.T) {
	d := contracts.DefaultPreferenceDisplay()
	raw, _ := json.Marshal(d)
	good := `{"contractVersion":2,"expectedRevision":0,"group":"display","display":` + string(raw) + `}`
	var request PreferenceRequest
	if security.DecodeJSON(strings.NewReader(good), 16384, &request) != nil || request.Validate() != nil {
		t.Fatal("valid display rejected")
	}
	for _, field := range []string{"units", "colorMode", "detail", "labels", "percentages", "hierarchyBorders", "reducedMotion"} {
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		delete(fields, field)
		missing, _ := json.Marshal(fields)
		request = PreferenceRequest{}
		input := strings.Replace(good, string(raw), string(missing), 1)
		if security.DecodeJSON(strings.NewReader(input), 16384, &request) == nil && request.Validate() == nil {
			t.Fatal("missing", field)
		}
	}
	for _, replacement := range []struct{ from, to string }{
		{`"labels":true`, `"labels":null`}, {`"labels":true`, `"labels":0`}, {`"labels":true`, `"labels":"false"`},
		{`"percentages":true`, `"percentages":null`}, {`"detail":"auto"`, `"detail":0`}, {`"detail":"auto"`, `"detail":9`}, {`"detail":"auto"`, `"detail":"1"`}, {`"detail":"auto"`, `"detail":1.0`},
		{`"colorMode":"folder"`, `"colorMode":"size"`}, {`"hierarchyBorders":"normal"`, `"hierarchyBorders":"off"`}, {`"units":"decimal"`, `"units":"auto"`}, {`"reducedMotion":"auto"`, `"reducedMotion":"none"`},
		{`"contractVersion":2`, `"contractVersion":1`}, {`"group":"display"`, `"group":"appearance"`}, {`"units":"decimal"`, `"extra":true,"units":"decimal"`},
	} {
		request = PreferenceRequest{}
		input := strings.Replace(good, replacement.from, replacement.to, 1)
		if security.DecodeJSON(strings.NewReader(input), 16384, &request) == nil && request.Validate() == nil {
			t.Fatal(input)
		}
	}
	for _, depth := range []string{`"auto"`, `"max"`, `1`, `2`, `3`, `4`, `5`, `6`, `7`, `8`} {
		input := strings.Replace(good, `"detail":"auto"`, `"detail":`+depth, 1)
		input = strings.ReplaceAll(input, `:true`, `:false`)
		request = PreferenceRequest{}
		if security.DecodeJSON(strings.NewReader(input), 16384, &request) != nil || request.Validate() != nil {
			t.Fatal(input)
		}
	}
}

// Display reset is an ordinary exact CAS intent and preserves every other personal group.
func TestPreferenceDisplayCASResetIsolation(t *testing.T) {
	m, in, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	a, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "appearance")
	if e != nil {
		t.Fatal(e)
	}
	view := contracts.PreferenceView{ProfileID: in.Profile.ID, SnapshotID: "snapshot", RootID: "nas", Map: "radial", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	a, e = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: 2, ExpectedRevision: 1, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "view")
	if e != nil {
		t.Fatal(e)
	}
	d := contracts.DefaultPreferenceDisplay()
	d.Units = "binary"
	d.Labels = false
	d.Percentages = false
	d.Detail = "8"
	d.ColorMode = "filetype"
	d.HierarchyBorders = "subtle"
	d.ReducedMotion = "reduce"
	req := PreferenceRequest{ContractVersion: 2, ExpectedRevision: 2, Group: "display", Display: &d}
	b, e := m.SetPreferences(ctx, req, "admin", "display")
	if e != nil || b.Display != d {
		t.Fatal(b, e)
	}
	again, e := m.SetPreferences(ctx, req, "admin", "display")
	if e != nil || !reflect.DeepEqual(b, again) {
		t.Fatal(again, e)
	}
	if _, e = m.SetPreferences(ctx, req, "admin", "stale"); !errors.Is(e, ErrPreferenceConflict) {
		t.Fatal(e)
	}
	d.Labels = true
	if _, e = m.SetPreferences(ctx, req, "admin", "display"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	d = contracts.DefaultPreferenceDisplay()
	req.ExpectedRevision = 3
	b, e = m.SetPreferences(ctx, req, "admin", "reset")
	if e != nil {
		t.Fatal(e)
	}
	a.Revision = 4
	if !reflect.DeepEqual(a, b) {
		t.Fatal("reset changed unrelated group", a, b)
	}
	other, e := m.Preferences(ctx, "other")
	if e != nil || other.Revision != 0 || other.Display != d {
		t.Fatal(other, e)
	}
}

// Retained timestamp choices remain readable, but new intents cannot select absent metadata.
func TestPreferenceDisplayRemovedTimestampChoice(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	d := contracts.DefaultPreferenceDisplay()
	d.ColorMode = "mtime"
	req := PreferenceRequest{ContractVersion: 2, Group: "display", Display: &d}
	if _, err := m.SetPreferences(ctx, req, "admin", "new-mtime"); !errors.Is(err, ErrInvalid) {
		t.Fatal("new timestamp choice accepted", err)
	}
	before, err := m.Preferences(ctx, "admin")
	if err != nil || before.Revision != 0 {
		t.Fatal("rejected intent changed preferences", before, err)
	}
	// Seed an actual persisted choice from a previous version without bypassing read validation.
	remembered := defaultPreferences("admin")
	remembered.Revision = 1
	remembered.Display = d
	raw, err := json.Marshal(remembered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`INSERT INTO user_preferences(principal,revision,value_json) VALUES(?,?,?)`, "admin", 1, string(raw)); err != nil {
		t.Fatal(err)
	}
	actual, err := m.Preferences(ctx, "admin")
	if err != nil || !reflect.DeepEqual(actual, remembered) || !ValidPreference(actual, "admin") {
		t.Fatal("remembered choice unreadable", actual, err)
	}
	d = contracts.DefaultPreferenceDisplay()
	req.ExpectedRevision = 1
	reset, err := m.SetPreferences(ctx, req, "admin", "reset-mtime")
	if err != nil || reset.Revision != 2 || reset.Display != d {
		t.Fatal("remembered choice cannot reset", reset, err)
	}
	readback, err := m.Preferences(ctx, "admin")
	if err != nil || !reflect.DeepEqual(reset, readback) {
		t.Fatal(readback, err)
	}
}

// The real offline path preserves domain rows, identity, revision, payload bytes and source indexes.
func TestPreferenceDisplayV6Upgrade(t *testing.T) {
	m, in, _ := fixture(t)
	ctx := context.Background()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	expected, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "old-key")
	if e != nil {
		t.Fatal(e)
	}
	snapshot := scanRetention(t, m, in.Profile.ID)
	appearanceReceipt := expected
	view := contracts.PreferenceView{ProfileID: in.Profile.ID, SnapshotID: snapshot, RootID: "nas", Map: "radial", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	expected, e = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: 2, ExpectedRevision: 1, Group: "lastView", View: &PreferenceViewChange{Value: &view}}, "admin", "remembered-view")
	if e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	db, e := sql.Open("sqlite", filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	downgradePreferenceFixture(t, db)
	if _, e = db.Exec("DELETE FROM schema_steps WHERE version=7;PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	var payload, oldReceipt string
	if e = db.QueryRow("SELECT payload,result_json FROM idempotency WHERE operation='preferences.update' AND key='old-key'").Scan(&payload, &oldReceipt); e != nil {
		t.Fatal(e)
	}
	profileBefore := preferenceTableDump(t, db, "profiles")
	jobsBefore := preferenceTableDump(t, db, "jobs")
	db.Close()
	sentinel := filepath.Join(state, "generations", snapshot, "index.db")
	indexBefore, e := os.ReadFile(sentinel)
	if e != nil {
		t.Fatal(e)
	}
	indexHash := sha256.Sum256(indexBefore)
	if opened, e := Open(ctx, state); !errors.Is(e, migration.ErrVersion) {
		if opened != nil {
			opened.Close()
		}
		t.Fatal(e)
	}
	for _, mode := range []string{"prepare", "apply"} {
		if e = Upgrade(ctx, state, mode); e != nil {
			t.Fatal(mode, e)
		}
	}
	m, e = Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	actual, e := m.Preferences(ctx, "admin")
	if e != nil || !reflect.DeepEqual(actual, expected) {
		t.Fatal(actual, e)
	}
	var keptPayload, newReceipt string
	m.db.QueryRow("SELECT payload,result_json FROM idempotency WHERE operation='preferences.update' AND principal='admin' AND key='old-key'").Scan(&keptPayload, &newReceipt)
	if keptPayload != payload || newReceipt == oldReceipt {
		t.Fatal("migration changed intent or omitted receipt upgrade")
	}
	var receipt contracts.UserPreference
	if security.DecodeJSON(strings.NewReader(newReceipt), 16384, &receipt) != nil || !reflect.DeepEqual(receipt, appearanceReceipt) {
		t.Fatal(receipt)
	}
	if preferenceTableDump(t, m.db, "profiles") != profileBefore || preferenceTableDump(t, m.db, "jobs") != jobsBefore {
		t.Fatal("domain rows changed")
	}
	if raw, _ := os.ReadFile(sentinel); sha256.Sum256(raw) != indexHash {
		t.Fatal("index bytes changed")
	}
	if _, e = m.SetPreferences(ctx, prefAppearance(0), "admin", "old-key"); e != ErrIdempotencyConflict {
		t.Fatal("historical key reused", e)
	}
	// Old requests are rejected, not re-executed under a compatibility fallback.
	oldRequest := prefAppearance(0)
	oldRequest.ContractVersion = 1
	if _, e = m.SetPreferences(ctx, oldRequest, "admin", "old-key"); e != ErrInvalid {
		t.Fatal(e)
	}
}

// A late receipt error or injected write failure rolls back rows and version together.
func TestPreferenceDisplayMigrationAtomicFailure(t *testing.T) {
	for _, failure := range []string{"foreign-owner", "wrong-revision", "wrong-group", "trigger"} {
		t.Run(failure, func(t *testing.T) {
			m, _, _ := fixture(t)
			defer m.Close()
			ctx := context.Background()
			if _, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "key"); e != nil {
				t.Fatal(e)
			}
			downgradePreferenceFixture(t, m.db)
			m.db.Exec("DELETE FROM schema_steps WHERE version=7;PRAGMA user_version=6")
			switch failure {
			case "foreign-owner":
				m.db.Exec(`UPDATE idempotency SET result_json=json_set(result_json,'$.dsmUserId','foreign')`)
			case "wrong-revision":
				m.db.Exec(`UPDATE idempotency SET result_json=json_set(result_json,'$.revision',2)`)
			case "wrong-group":
				m.db.Exec(`UPDATE idempotency SET result_json=json_set(result_json,'$.language','en')`)
			case "trigger":
				m.db.Exec(`CREATE TRIGGER injected_failure BEFORE UPDATE ON idempotency BEGIN SELECT RAISE(ABORT,'test'); END`)
			}
			before := preferenceTableDump(t, m.db, "user_preferences")
			receipts := preferenceTableDump(t, m.db, "idempotency")
			if e := migratePreferenceDisplay(ctx, m.db); e == nil {
				t.Fatal("accepted invalid migration")
			}
			var version int
			m.db.QueryRow("PRAGMA user_version").Scan(&version)
			if version != 6 || before != preferenceTableDump(t, m.db, "user_preferences") || receipts != preferenceTableDump(t, m.db, "idempotency") {
				t.Fatal("partial migration")
			}
		})
	}
}

// Candidate failure after the new migration still restores the exact schema-6 source bytes.
func TestPreferenceDisplayV6FailureRestore(t *testing.T) {
	m, _, _ := fixture(t)
	ctx := context.Background()
	if _, e := m.SetPreferences(ctx, prefAppearance(0), "admin", "key"); e != nil {
		t.Fatal(e)
	}
	state := m.state
	m.Close()
	db, _ := sql.Open("sqlite", filepath.Join(state, "control.db"))
	downgradePreferenceFixture(t, db)
	if _, e := db.Exec("DELETE FROM schema_steps WHERE version=7;PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	original, e := os.ReadFile(filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if e = Upgrade(ctx, state, "prepare"); e != nil {
		t.Fatal(e)
	}
	e = migration.Apply(ctx, state, func(ctx context.Context, db *sql.DB) error {
		if e := initializeControl(ctx, db); e != nil {
			return e
		}
		return errors.New("injected after migration")
	})
	if e == nil {
		t.Fatal("candidate failure accepted")
	}
	if e = Upgrade(ctx, state, "restore"); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(state, "control.db"))
	if e != nil || !reflect.DeepEqual(original, after) {
		t.Fatal("restore changed source", e)
	}
}

// Signed rowids and multiple batches cannot silently omit any personal row or receipt.
func TestPreferenceDisplayMigrationBatches(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	ctx := context.Background()
	v := defaultPreferences("base")
	v.Revision = 1
	raw, _ := json.Marshal(v)
	request := prefAppearance(0)
	request.ContractVersion = 1
	payload, _ := json.Marshal(request)
	// The receipt must describe the submitted appearance group exactly.
	v.Language = "de"
	v.Theme = "dark"
	v.Map = "radial"
	raw, _ = json.Marshal(v)
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 130; i++ {
		id := int64(i - 1)
		if i == 129 {
			id = 9223372036854775807
		}
		if i == 0 {
			id = -9223372036854775808
		}
		principal := fmt.Sprintf("admin-%03d", i)
		var fields map[string]any
		json.Unmarshal(raw, &fields)
		delete(fields, "display")
		fields["version"] = 1
		fields["dsmUserId"] = principal
		body, _ := json.Marshal(fields)
		if _, e = tx.Exec("INSERT INTO user_preferences(rowid,principal,revision,value_json) VALUES(?,?,1,?)", id, principal, string(body)); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO idempotency(rowid,principal,operation,key,payload,result_json) VALUES(?,?,'preferences.update','key',?,?)", id, principal, string(payload), string(body)); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec("DELETE FROM schema_steps WHERE version=7;PRAGMA user_version=6"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	if e = migratePreferenceDisplay(ctx, m.db); e != nil {
		t.Fatal(e)
	}
	for _, table := range []string{"user_preferences", "idempotency"} {
		column := "value_json"
		if table == "idempotency" {
			column = "result_json"
		}
		var count int
		if e = m.db.QueryRow("SELECT count(*) FROM " + table + " WHERE json_extract(" + column + ",'$.version')=2 AND json_extract(" + column + ",'$.display.labels')=1").Scan(&count); e != nil || count != 130 {
			t.Fatal(table, count, e)
		}
	}
}
