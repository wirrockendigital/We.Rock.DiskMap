package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ErrConfigurationTooLarge requires an explicit failure rather than an incomplete backup.
var ErrConfigurationTooLarge = errors.New("CONFIGURATION_TOO_LARGE")

// ValidConfigurationBundle checks the portable domain without consulting current inventory.
// Source IDs are references for a later explicit import mapping, never access authority.
func ValidConfigurationBundle(v contracts.ConfigurationBundle) bool {
	if v.FormatVersion != 1 || len(v.ExportedAt) > 64 || v.Profiles == nil || len(v.Profiles) > contracts.ConfigurationProfileLimit {
		return false
	}
	if _, e := time.Parse(time.RFC3339Nano, v.ExportedAt); e != nil {
		return false
	}
	seen := map[string]bool{}
	for _, p := range v.Profiles {
		c := p.Configuration
		if seen[p.SourceProfileID] || c.Scope.TargetIDs == nil || c.Exclusions == nil {
			return false
		}
		seen[p.SourceProfileID] = true
		known := map[string]bool{}
		for _, id := range c.Scope.TargetIDs {
			known[id] = true
		}
		if profiles.Validate(contracts.Profile{ID: p.SourceProfileID, Revision: 1, Name: c.Name, Scope: c.Scope, Active: c.Active, Exclusions: c.Exclusions, ResourceMode: c.ResourceMode, Limits: c.Limits, FreshnessSeconds: c.FreshnessSeconds}, known) != nil {
			return false
		}
	}
	if v.Global.ExclusionTemplate.Rules == nil {
		return false
	}
	rules, e := profiles.EffectiveExclusions(nil, v.Global.ExclusionTemplate.Rules)
	if e != nil || !reflect.DeepEqual(rules, v.Global.ExclusionTemplate.Rules) {
		return false
	}
	switch v.Global.RetentionCompleteCount {
	case 1, 2, 3, 7, 30:
	default:
		return false
	}
	mapping, e := index.CanonicalFileTypeMapping(v.Global.FileTypeMapping)
	if e != nil || !reflect.DeepEqual(mapping, v.Global.FileTypeMapping) {
		return false
	}
	a := v.Personal.Appearance
	if (PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "appearance", Appearance: &a}).Validate() != nil || !validPreferenceDisplay(v.Personal.Display) {
		return false
	}
	// A deleted preferred profile remains an unresolved source reference, not a substituted profile.
	if p := v.Personal.PreferredProfileRef; p != nil && !security.ValidID(*p) {
		return false
	}
	raw, e := json.Marshal(v)
	return e == nil && len(raw) <= contracts.ConfigurationBundleLimit
}

// ConfigurationExport reads only Control configuration in one cancellable read transaction.
// Whole-transaction BUSY retries discard every value from the previous attempt.
func (m *Manager) ConfigurationExport(parent context.Context, principal string) (contracts.ConfigurationExport, error) {
	if !validPrincipal(principal) {
		return contracts.ConfigurationExport{}, ErrInvalid
	}
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	out, e := retryControlRead(ctx, func() (contracts.ConfigurationExport, error) { return m.configurationExport(ctx, principal) })
	if errors.Is(e, ErrConfigurationTooLarge) {
		return contracts.ConfigurationExport{}, e
	}
	return out, observationError(e)
}

// configurationExport bounds rows before allocating their JSON and never reads source paths or indexes.
func (m *Manager) configurationExport(ctx context.Context, principal string) (contracts.ConfigurationExport, error) {
	var zero contracts.ConfigurationExport
	tx, e := m.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return zero, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT id,revision,CASE WHEN length(CAST(profile_json AS BLOB))<=4194304 THEN profile_json END FROM profiles ORDER BY id LIMIT 201`)
	if e != nil {
		return zero, e
	}
	out := contracts.ConfigurationExport{Bundle: contracts.ConfigurationBundle{FormatVersion: 1, ExportedAt: now(), Profiles: []contracts.ConfigurationProfile{}}}
	// The cumulative bound prevents excessive allocation before final JSON encoding.
	bytesRead := 0
	for rows.Next() {
		var id, raw string
		var revision int
		if e = rows.Scan(&id, &revision, &raw); e != nil {
			rows.Close()
			return zero, e
		}
		if len(out.Bundle.Profiles) >= contracts.ConfigurationProfileLimit || bytesRead > contracts.ConfigurationBundleLimit {
			rows.Close()
			return zero, ErrConfigurationTooLarge
		}
		var p contracts.Profile
		if security.DecodeJSON(bytes.NewBufferString(raw), contracts.ConfigurationBundleLimit, &p) != nil || p.ID != id || p.Revision != revision || revision < 1 {
			rows.Close()
			return zero, ErrUnavailable
		}
		c := contracts.ConfigurationProfileSettings{Name: p.Name, Scope: p.Scope, Active: p.Active, Exclusions: p.Exclusions, ResourceMode: p.ResourceMode, Limits: p.Limits, FreshnessSeconds: p.FreshnessSeconds}
		// Older valid profile rows may encode empty lists as null; the export format uses explicit arrays.
		if c.Scope.TargetIDs == nil {
			c.Scope.TargetIDs = []string{}
		}
		if c.Exclusions == nil {
			c.Exclusions = []contracts.Exclusion{}
		}
		record := contracts.ConfigurationProfile{SourceProfileID: id, Configuration: c}
		encoded, err := json.Marshal(record)
		if err != nil {
			rows.Close()
			return zero, err
		}
		bytesRead += len(encoded)
		if bytesRead > contracts.ConfigurationBundleLimit {
			rows.Close()
			return zero, ErrConfigurationTooLarge
		}
		out.Bundle.Profiles = append(out.Bundle.Profiles, record)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return zero, e
	}
	var bounded bool
	e = tx.QueryRowContext(ctx, `SELECT
      (SELECT length(CAST(value_json AS BLOB))<=16384 FROM exclusion_template WHERE singleton=1)
      AND (SELECT length(CAST(mapping_json AS BLOB))<=32768 FROM file_type_mapping WHERE singleton=1)
      AND COALESCE((SELECT length(CAST(value_json AS BLOB))<=16384 FROM user_preferences WHERE principal=?),1)`, principal).Scan(&bounded)
	if e != nil {
		return zero, e
	}
	if !bounded {
		return zero, ErrUnavailable
	}
	template, e := readExclusionTemplate(ctx, tx)
	if e != nil {
		return zero, e
	}
	out.Bundle.Global.ExclusionTemplate = contracts.ConfigurationTemplate{Rules: template.Rules}
	if e = tx.QueryRowContext(ctx, `SELECT complete_count FROM retention_settings WHERE singleton=1`).Scan(&out.Bundle.Global.RetentionCompleteCount); e != nil {
		return zero, e
	}
	out.Bundle.Global.FileTypeMapping, e = readFileTypeMapping(ctx, tx)
	if e != nil {
		return zero, e
	}
	pref, e := readPreferences(ctx, tx, principal)
	if e != nil {
		return zero, e
	}
	out.Bundle.Personal = contracts.ConfigurationPersonal{Appearance: contracts.PreferenceAppearance{Language: pref.Language, Theme: pref.Theme, Map: pref.Map, Locale: pref.Locale, TimeZone: pref.TimeZone}, Display: pref.Display, PreferredProfileRef: pref.PreferredProfileID}
	raw, e := json.Marshal(out.Bundle)
	if e != nil {
		return zero, e
	}
	if len(raw) > contracts.ConfigurationBundleLimit {
		return zero, ErrConfigurationTooLarge
	}
	if !ValidConfigurationBundle(out.Bundle) {
		return zero, ErrUnavailable
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return out, nil
}
