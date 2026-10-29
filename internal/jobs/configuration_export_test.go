package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Export preserves private configuration labels while excluding every runtime/source authority.
func TestConfigurationExportPrivacyAndPrincipal(t *testing.T) {
	m, in, source := fixture(t)
	defer m.Close()
	in.Profile.Name = "Privat – Ärzte 🗂"
	in.Profile.Exclusions = []contracts.Exclusion{{Kind: "path", Value: "Privat/Vertrag.pdf"}}
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	pref := defaultPreferences("owner-secret")
	pref.Language = "de"
	pref.Revision = 1
	pref.Display.Units = "binary"
	pref.LastView = &contracts.PreferenceView{ProfileID: "probe", SnapshotID: "private-snapshot", RootID: "private-node", Map: "treemap", Metric: "logical", FilterMode: "highlight", Filter: contracts.PreferenceFilter{Categories: []string{}}, Search: contracts.PreferenceSearch{Field: "both", Scope: "recursive"}, Sort: "name", Direction: "asc"}
	raw, _ := json.Marshal(pref)
	if _, e := m.db.Exec(`INSERT INTO user_preferences(principal,revision,value_json) VALUES(?,?,?)`, pref.DSMUserID, 1, string(raw)); e != nil {
		t.Fatal(e)
	}
	for _, principal := range []string{"owner-secret", "other-admin"} {
		out, e := m.ConfigurationExport(context.Background(), principal)
		if e != nil {
			t.Fatal(e)
		}
		if !ValidConfigurationBundle(out.Bundle) || len(out.Bundle.Profiles) != 1 || out.Bundle.Profiles[0].Configuration.Name != in.Profile.Name {
			t.Fatal("configuration loss")
		}
		b, _ := json.Marshal(out)
		s := string(b)
		for _, forbidden := range []string{source, "owner-secret", "private-snapshot", "private-node", "lastView", "dsmUserId", "roots_json", "sourceBindings", "jobs", "indexes", "password"} {
			if strings.Contains(s, forbidden) {
				t.Fatal("runtime authority leaked", forbidden)
			}
		}
		if !strings.Contains(s, "Privat/Vertrag.pdf") {
			t.Fatal("configuration exclusion lost")
		}
		if (out.Bundle.Personal.Appearance.Language == "de") != (principal == "owner-secret") {
			t.Fatal("cross-principal settings")
		}
	}
	if _, e := m.ConfigurationExport(context.Background(), ""); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.ConfigurationExport(ctx, "owner-secret"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

// Corruption never produces a partial bundle or a configuration containing private unknown fields.
func TestConfigurationExportInvalidAndBounds(t *testing.T) {
	for _, bad := range []string{`{"privateBinding":"/secret"}`, strings.Repeat("x", 131073)} {
		m, in, _ := fixture(t)
		if e := m.ImportProfile(in); e != nil {
			t.Fatal(e)
		}
		if _, e := m.db.Exec(`UPDATE profiles SET profile_json=?`, bad); e != nil {
			t.Fatal(e)
		}
		out, e := m.ConfigurationExport(context.Background(), "a")
		if !errors.Is(e, ErrUnavailable) || len(out.Bundle.Profiles) != 0 {
			t.Fatal("partial corrupt result", e)
		}
		m.Close()
	}
	m, in, _ := fixture(t)
	defer m.Close()
	// Real legal configurations can exceed the aggregate export size; no rows are silently dropped.
	for i := 0; i < 201; i++ {
		p := in.Profile
		p.ID = fmt.Sprintf("p-%03d", i)
		p.Exclusions = []contracts.Exclusion{{Kind: "path", Value: strings.Repeat("&", 4096)}, {Kind: "path", Value: "a" + strings.Repeat("&", 4095)}, {Kind: "path", Value: "b" + strings.Repeat("&", 4095)}, {Kind: "path", Value: "c" + strings.Repeat("&", 4095)}}
		raw, _ := json.Marshal(p)
		if _, e := m.db.Exec(`INSERT INTO profiles VALUES(?,1,?,'{}','{}')`, p.ID, string(raw)); e != nil {
			t.Fatal(e)
		}
	}
	out, e := m.ConfigurationExport(context.Background(), "a")
	if !errors.Is(e, ErrConfigurationTooLarge) || len(out.Bundle.Profiles) != 0 {
		t.Fatal("oversize must fail atomically", e)
	}
}

// A held writer connection does not block the independent read transaction; closed managers fail explicitly.
func TestConfigurationExportReadPoolAndClose(t *testing.T) {
	m, _, _ := fixture(t)
	conn, e := m.db.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.ConfigurationExport(context.Background(), "a"); e != nil {
		t.Fatal(e)
	}
	conn.Close()
	m.Close()
	if _, e = m.ConfigurationExport(context.Background(), "a"); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}

// Concurrent atomic updates cannot mix a profile from one revision with another revision's global settings.
func TestConfigurationExportConsistentTransaction(t *testing.T) {
	m, in, _ := fixture(t)
	defer m.Close()
	in.Profile.Name = "retention-2"
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 20; i++ {
			count := 2
			if i%2 == 0 {
				count = 7
			}
			p := in.Profile
			p.Name = fmt.Sprintf("retention-%d", count)
			raw, _ := json.Marshal(p)
			tx, e := m.db.BeginTx(context.Background(), nil)
			if e != nil {
				done <- e
				return
			}
			if _, e = tx.Exec(`UPDATE profiles SET profile_json=? WHERE id='probe'`, string(raw)); e == nil {
				_, e = tx.Exec(`UPDATE retention_settings SET complete_count=?`, count)
			}
			if e == nil {
				e = tx.Commit()
			} else {
				tx.Rollback()
			}
			if e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	for i := 0; i < 20; i++ {
		out, e := m.ConfigurationExport(context.Background(), "a")
		if e != nil {
			t.Fatal(e)
		}
		if out.Bundle.Profiles[0].Configuration.Name != fmt.Sprintf("retention-%d", out.Bundle.Global.RetentionCompleteCount) {
			t.Fatal("mixed transaction")
		}
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
