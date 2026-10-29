package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// importFixture uses the real native-catalogue seam and keeps an existing profile with the colliding source ID.
func importFixture(t *testing.T) (*Manager, contracts.ConfigurationImportRequest, *profileSources) {
	t.Helper()
	m, _, _, catalog := nativeFixture(t, "volumes")
	out, e := m.ConfigurationExport(context.Background(), "admin")
	if e != nil {
		t.Fatal(e)
	}
	target := "volume-a"
	in := contracts.ConfigurationImportRequest{ContractVersion: 1, Bundle: out.Bundle, Selection: contracts.ConfigurationSelection{ProfileIDs: []string{"probe"}}, Mappings: []contracts.ConfigurationTargetMapping{{SourceTargetID: "volume-a", TargetID: &target}}}
	return m, in, catalog
}

// Preview does not mutate; explicit mappings create new inactive profiles, never redirect existing tasks or source IDs.
func TestConfigurationImportPreviewApplyReplay(t *testing.T) {
	m, in, catalog := importFixture(t)
	defer m.Close()
	ctx := context.Background()
	empty := in
	empty.Mappings = []contracts.ConfigurationTargetMapping{}
	preview, e := m.PreviewConfigurationImport(ctx, empty, "admin")
	if e != nil || preview.CanApply || len(preview.UnassignedTargetIDs) != 1 {
		t.Fatal(preview, e)
	}
	var count int
	m.db.QueryRow(`SELECT count(*) FROM profiles`).Scan(&count)
	if count != 1 {
		t.Fatal("preview mutated")
	}
	preview, e = m.PreviewConfigurationImport(ctx, in, "admin")
	if e != nil || !preview.CanApply {
		t.Fatal(preview, e)
	}
	in.Expected = &preview.Expected
	receipt, e := m.ApplyConfigurationImport(ctx, in, "admin", "import-1")
	if e != nil || !ValidConfigurationImportReceipt(receipt, in) {
		t.Fatal(receipt, e)
	}
	var raw string
	m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id=?`, receipt.Created[0].ProfileID).Scan(&raw)
	var p contracts.Profile
	json.Unmarshal([]byte(raw), &p)
	if p.Active || p.ID == "probe" || p.Revision != 1 {
		t.Fatal(p)
	}
	m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count)
	if count != 0 {
		t.Fatal("import started scan")
	}
	// Recovery resolves its committed receipt before attempting a fresh inventory observation.
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return profileSources{}, ErrUnavailable }
	replay, e := m.ApplyConfigurationImport(ctx, in, "admin", "import-1")
	if e != nil || !reflect.DeepEqual(replay, receipt) {
		t.Fatal(replay, e)
	}
	changed := in
	changed.Bundle.ExportedAt = "2026-10-10T00:00:00Z"
	if _, e = m.ApplyConfigurationImport(ctx, changed, "admin", "import-1"); !errors.Is(e, ErrIdempotencyConflict) {
		t.Fatal(e)
	}
	m.catalog = func(context.Context, profileSources) (profileSources, error) { return detachedCatalog(*catalog), nil }
	if _, e = m.ApplyConfigurationImport(ctx, in, "other", "import-1"); !errors.Is(e, ErrImportConflict) {
		t.Fatal("cross-principal replay", e)
	}
}

// Stale CAS, changed targets and late SQL failures all leave the original profiles and preferences intact.
func TestConfigurationImportRollbackAndCAS(t *testing.T) {
	for _, mode := range []string{"cas", "target", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			m, in, catalog := importFixture(t)
			defer m.Close()
			ctx := context.Background()
			in.Selection.Settings.Display = true
			in.Bundle.Personal.Display.Units = "binary"
			preview, e := m.PreviewConfigurationImport(ctx, in, "admin")
			if e != nil {
				t.Fatal(e)
			}
			in.Expected = &preview.Expected
			switch mode {
			case "cas":
				if _, e = m.SetPreferences(ctx, PreferenceRequest{ContractVersion: 2, Group: "display", ExpectedRevision: 0, Display: &in.Bundle.Personal.Display}, "admin", "other"); e != nil {
					t.Fatal(e)
				}
			case "target":
				catalog.Volumes[0].ID = "volume-other"
			case "rollback":
				if _, e = m.db.Exec(`CREATE TRIGGER import_fail BEFORE INSERT ON idempotency WHEN NEW.operation='configuration.import' BEGIN SELECT RAISE(ABORT,'fixture'); END`); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = m.ApplyConfigurationImport(ctx, in, "admin", "apply"); e == nil {
				t.Fatal("accepted invalid apply")
			}
			var count int
			m.db.QueryRow(`SELECT count(*) FROM profiles`).Scan(&count)
			if count != 1 {
				t.Fatal("partial profiles")
			}
			p, e := m.Preferences(ctx, "admin")
			if e != nil {
				t.Fatal(e)
			}
			if mode != "cas" && (p.Revision != 0 || p.Display.Units != "decimal") {
				t.Fatal("partial preferences")
			}
		})
	}
}

// The standalone schema remains strict and selected missing targets cannot disappear silently.
func TestConfigurationImportStrictAndMissing(t *testing.T) {
	m, in, _ := importFixture(t)
	defer m.Close()
	raw, _ := json.Marshal(in)
	var decoded contracts.ConfigurationImportRequest
	if e := DecodeConfigurationImport(raw, &decoded, false); e != nil {
		t.Fatal(e)
	}
	bad := append([]byte(nil), raw...)
	bad = append(bad, []byte(` {}`)...)
	if DecodeConfigurationImport(bad, &decoded, false) == nil {
		t.Fatal("trailing JSON")
	}
	in.Bundle.FormatVersion = 2
	if ValidateConfigurationImport(in, false) == nil {
		t.Fatal("future version")
	}
	in.Bundle.FormatVersion = 1
	in.Mappings = []contracts.ConfigurationTargetMapping{}
	preview, e := m.PreviewConfigurationImport(context.Background(), in, "admin")
	if e != nil {
		t.Fatal(e)
	}
	in.Expected = &preview.Expected
	if _, e = m.ApplyConfigurationImport(context.Background(), in, "admin", "missing"); !errors.Is(e, ErrImportUnmapped) {
		t.Fatal(e)
	}
}

// Maximum-length source references must never be duplicated into persisted replay results.
func TestConfigurationImportCompactMaximumReceipt(t *testing.T) {
	m, in, _ := importFixture(t)
	defer m.Close()
	in.Bundle.Profiles = nil
	in.Selection.ProfileIDs = []string{}
	out := contracts.ConfigurationImportReceipt{Created: []contracts.ConfigurationCreatedProfile{}, TasksRequireReview: true}
	for i := 0; i < 200; i++ {
		source := fmt.Sprintf("%0128d", i)
		destination := fmt.Sprintf("%032x", i+1000)
		in.Bundle.Profiles = append(in.Bundle.Profiles, contracts.ConfigurationProfile{SourceProfileID: source})
		in.Selection.ProfileIDs = append(in.Selection.ProfileIDs, source)
		out.Created = append(out.Created, contracts.ConfigurationCreatedProfile{SourceProfileID: source, ProfileID: destination})
	}
	out.BundleDigest = configurationDigest(in.Bundle)
	out.SelectionDigest = importSelectionDigest(in)
	raw, e := encodeImportReceipt(out)
	if e != nil || len(raw) > 16384 {
		t.Fatal(len(raw), e)
	}
	decoded, e := decodeImportReceipt(raw, in)
	if e != nil || !reflect.DeepEqual(decoded, out) {
		t.Fatal(e)
	}
	in.Selection.ProfileIDs[0], in.Selection.ProfileIDs[1] = in.Selection.ProfileIDs[1], in.Selection.ProfileIDs[0]
	if _, e = decodeImportReceipt(raw, in); e == nil {
		t.Fatal("reordered intent accepted")
	}
}

// Selected settings commit together, preserve the remembered view, and do not modify existing profile rows.
func TestConfigurationImportAllSettingsAndRestart(t *testing.T) {
	m, in, _ := importFixture(t)
	defer m.Close()
	ctx := context.Background()
	pref, e := m.Preferences(ctx, "admin")
	if e != nil {
		t.Fatal(e)
	}
	pref.Revision = 1
	pref.LastView = &contracts.PreferenceView{ProfileID: "probe", SnapshotID: "snapshot", RootID: "nas", Map: "treemap", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	if !ValidPreference(pref, "admin") {
		t.Fatal("invalid preference fixture")
	}
	raw, _ := json.Marshal(pref)
	if _, e = m.db.Exec(`INSERT INTO user_preferences(principal,revision,value_json) VALUES('admin',1,?)`, string(raw)); e != nil {
		t.Fatal(e)
	}
	var old string
	if e = m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id='probe'`).Scan(&old); e != nil {
		t.Fatal(e)
	}
	in.Selection.Settings = contracts.ConfigurationSettingsSelection{Appearance: true, Display: true, PreferredProfile: true, ExclusionTemplate: true, Retention: true, FileTypeMapping: true}
	in.Bundle.Personal.Appearance.Language = "de"
	in.Bundle.Personal.Display.Units = "binary"
	ref := "probe"
	in.Bundle.Personal.PreferredProfileRef = &ref
	in.Bundle.Global.RetentionCompleteCount = 7
	in.Bundle.Global.ExclusionTemplate.Rules = []contracts.Exclusion{{Kind: "pattern", Value: "*.tmp"}}
	preview, e := m.PreviewConfigurationImport(ctx, in, "admin")
	if e != nil || !preview.CanApply {
		t.Fatal(e, preview)
	}
	in.Expected = &preview.Expected
	receipt, e := m.ApplyConfigurationImport(ctx, in, "admin", "settings-import")
	if e != nil {
		t.Fatal(e)
	}
	got, e := m.Preferences(ctx, "admin")
	if e != nil || got.Language != "de" || got.Display.Units != "binary" || got.Revision != 2 || !reflect.DeepEqual(got.LastView, pref.LastView) || got.PreferredProfileID == nil || *got.PreferredProfileID != receipt.Created[0].ProfileID {
		t.Fatal(got, e)
	}
	var after string
	m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id='probe'`).Scan(&after)
	if old != after {
		t.Fatal("existing profile changed")
	}
	exported, e := m.ConfigurationExport(ctx, "admin")
	if e != nil || exported.Bundle.Global.RetentionCompleteCount != 7 || !reflect.DeepEqual(exported.Bundle.Global.ExclusionTemplate.Rules, in.Bundle.Global.ExclusionTemplate.Rules) {
		t.Fatal(e)
	}
	state := m.state
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	replay, e := reopened.ApplyConfigurationImport(ctx, in, "admin", "settings-import")
	if e != nil || !reflect.DeepEqual(replay, receipt) {
		t.Fatal(e, replay)
	}
}

// Existing scheduler profile references and captured terminal jobs remain byte-identical after import.
func TestConfigurationImportPreservesTaskBindings(t *testing.T) {
	m, in, _ := importFixture(t)
	defer m.Close()
	ctx := context.Background()
	var profileJSON string
	if e := m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id='probe'`).Scan(&profileJSON); e != nil {
		t.Fatal(e)
	}
	var profile contracts.Profile
	json.Unmarshal([]byte(profileJSON), &profile)
	prior := Outcome{Job: contracts.Job{ID: "scheduled-before-import", ProfileID: profile.ID, ProfileRevision: profile.Revision, CapturedProfile: profile, Trigger: "cli", State: "completed", CreatedAt: "2026-10-11T00:00:00Z", EntriesVisited: "0", BytesObserved: "0", Warnings: []contracts.APIError{}}}
	raw, _ := json.Marshal(prior)
	if _, e := m.db.Exec(`INSERT INTO jobs(id,state,outcome_json) VALUES(?,'completed',?)`, prior.Job.ID, string(raw)); e != nil {
		t.Fatal(e)
	}
	oldPayload := `{"profileId":"probe","profileRevision":1,"scanMode":"rebuild"}`
	if _, e := m.db.Exec(`INSERT INTO idempotency(principal,operation,key,payload,result_json) VALUES('admin','scan.start','scheduled-existing',?,?)`, oldPayload, `{"jobId":"scheduled-before-import"}`); e != nil {
		t.Fatal(e)
	}
	preview, e := m.PreviewConfigurationImport(ctx, in, "admin")
	if e != nil {
		t.Fatal(e)
	}
	in.Expected = &preview.Expected
	result, e := m.ApplyConfigurationImport(ctx, in, "admin", "new-import")
	if e != nil || result.Created[0].ProfileID == "probe" {
		t.Fatal(e)
	}
	var job, profileAfter, payload, receipt string
	m.db.QueryRow(`SELECT outcome_json FROM jobs WHERE id=?`, prior.Job.ID).Scan(&job)
	m.db.QueryRow(`SELECT profile_json FROM profiles WHERE id='probe'`).Scan(&profileAfter)
	m.db.QueryRow(`SELECT payload,result_json FROM idempotency WHERE operation='scan.start' AND key='scheduled-existing'`).Scan(&payload, &receipt)
	if job != string(raw) || profileAfter != profileJSON || payload != oldPayload || receipt != `{"jobId":"scheduled-before-import"}` {
		t.Fatal("existing task reference changed")
	}
	var count int
	m.db.QueryRow(`SELECT count(*) FROM jobs`).Scan(&count)
	if count != 1 {
		t.Fatal("new job started")
	}
}

// Waiting for the writer pool must never retain the lifecycle lock needed by unrelated admissions.
func TestConfigurationImportHeldWriterAdmission(t *testing.T) {
	m, in, _ := importFixture(t)
	defer m.Close()
	ctx := context.Background()
	preview, e := m.PreviewConfigurationImport(ctx, in, "admin")
	if e != nil {
		t.Fatal(e)
	}
	in.Expected = &preview.Expected
	conn, e := m.db.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	before := m.db.Stats().WaitCount
	request, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := m.ApplyConfigurationImport(request, in, "admin", "held-writer"); done <- e }()
	deadline := time.Now().Add(400 * time.Millisecond)
	for m.db.Stats().WaitCount == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.db.Stats().WaitCount == before {
		t.Fatal("import never reached writer admission")
	}
	if !m.mu.TryLock() {
		t.Fatal("writer wait retained lifecycle lock")
	}
	m.mu.Unlock()
	cancel()
	if e := <-done; !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
	var count int
	if e = m.readDB.QueryRow(`SELECT count(*) FROM profiles`).Scan(&count); e != nil || count != 1 {
		t.Fatal(e, count)
	}
}
