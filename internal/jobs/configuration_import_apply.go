package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// preparedImportProfile contains private verified bindings only in request-local memory.
type preparedImportProfile struct {
	p        contracts.Profile
	sources  profileSources
	bindings map[string]scanner.RootBinding
	volumes  map[string]string
	known    map[string]bool
}

// prepareImportProfile uses the existing normalizer and native binding guards before taking the writer transaction.
func (m *Manager) prepareImportProfile(ctx context.Context, p contracts.Profile, s profileSources) (preparedImportProfile, error) {
	out := preparedImportProfile{p: p, sources: s, bindings: map[string]scanner.RootBinding{}, volumes: map[string]string{}, known: map[string]bool{}}
	n, e := normalizeSources(p.Scope, s)
	if e != nil {
		return out, ErrImportUnmapped
	}
	for _, id := range n.CoveredTargetIDs {
		out.known[id] = true
	}
	for _, r := range n.Roots {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		out.known[r.TargetID] = true
		out.volumes[r.TargetID] = r.VolumeID
		if !safeSourcePath(r.PhysicalPath, m.state, false) {
			return out, ErrInvalid
		}
		if r.State != "reachable" {
			return out, ErrImportUnmapped
		}
		fd, b, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return out, ErrUnavailable
		}
		fd.Close()
		out.bindings[r.TargetID] = b
	}
	if e = confirmNativeBindings(ctx, p, s, out.bindings, m.catalog); e != nil {
		return out, e
	}
	stampNativeBindings(&out.sources, p, out.bindings)
	return out, nil
}

// persistImportProfile writes one already prepared profile inside the single import transaction.
func persistImportProfile(ctx context.Context, tx *sql.Tx, p preparedImportProfile) (contracts.Profile, error) {
	roots, _ := json.Marshal(p.bindings)
	volumes, _ := json.Marshal(p.volumes)
	out, e := profiles.Write(ctx, tx, profiles.Record{Profile: p.p, RootsJSON: string(roots), VolumesJSON: string(volumes)}, 0, p.known)
	if e != nil {
		return out, profileError(e)
	}
	raw, _ := json.Marshal(p.sources)
	_, e = tx.ExecContext(ctx, `INSERT INTO profile_sources(profile_id,sources_json) VALUES(?,?)`, out.ID, string(raw))
	return out, e
}

// importExpectationMatches applies CAS only to explicitly selected groups and profile creation capacity.
func importExpectationMatches(a, b contracts.ConfigurationExpectation, s contracts.ConfigurationSelection) bool {
	g := s.Settings
	return (len(s.ProfileIDs) == 0 || a.ProfileCount == b.ProfileCount) && (!(g.Appearance || g.Display || g.PreferredProfile) || a.PreferenceRevision == b.PreferenceRevision) && (!g.ExclusionTemplate || a.TemplateRevision == b.TemplateRevision) && (!g.FileTypeMapping || a.MappingRevision == b.MappingRevision) && (!g.Retention || a.RetentionCompleteCount == b.RetentionCompleteCount)
}

// ValidConfigurationImportReceipt binds replay and transport success to the exact selected source references.
func ValidConfigurationImportReceipt(out contracts.ConfigurationImportReceipt, in contracts.ConfigurationImportRequest) bool {
	if out.BundleDigest != configurationDigest(in.Bundle) || out.SelectionDigest != importSelectionDigest(in) || !out.TasksRequireReview || out.SettingsApplied != in.Selection.Settings || out.Created == nil || len(out.Created) != len(in.Selection.ProfileIDs) {
		return false
	}
	seen := map[string]bool{}
	sources := map[string]bool{}
	for _, p := range in.Bundle.Profiles {
		sources[p.SourceProfileID] = true
	}
	for i, p := range out.Created {
		if p.SourceProfileID != in.Selection.ProfileIDs[i] || !security.ValidID(p.ProfileID) || sources[p.ProfileID] || seen[p.ProfileID] {
			return false
		}
		seen[p.ProfileID] = true
	}
	return true
}

// importStoredReceipt keeps the replay result below 16 KiB without duplicating source references.
type importStoredReceipt struct {
	BundleDigest    string   `json:"bundleDigest"`
	SelectionDigest string   `json:"selectionDigest"`
	ProfileIDs      []string `json:"profileIds"`
}

// encodeImportReceipt persists only generated IDs; their order is bound by the intent digest.
func encodeImportReceipt(out contracts.ConfigurationImportReceipt) ([]byte, error) {
	saved := importStoredReceipt{out.BundleDigest, out.SelectionDigest, []string{}}
	for _, p := range out.Created {
		saved.ProfileIDs = append(saved.ProfileIDs, p.ProfileID)
	}
	raw, e := json.Marshal(saved)
	if e != nil || len(raw) > 16384 {
		return nil, ErrUnavailable
	}
	return raw, nil
}

// decodeImportReceipt reconstructs public source references solely from the exact replay-bound request.
func decodeImportReceipt(raw []byte, in contracts.ConfigurationImportRequest) (contracts.ConfigurationImportReceipt, error) {
	var saved importStoredReceipt
	if security.DecodeJSON(bytes.NewReader(raw), 16384, &saved) != nil || saved.ProfileIDs == nil || len(saved.ProfileIDs) != len(in.Selection.ProfileIDs) {
		return contracts.ConfigurationImportReceipt{}, ErrUnavailable
	}
	out := contracts.ConfigurationImportReceipt{BundleDigest: saved.BundleDigest, SelectionDigest: saved.SelectionDigest, Created: []contracts.ConfigurationCreatedProfile{}, SettingsApplied: in.Selection.Settings, TasksRequireReview: true}
	for i, id := range saved.ProfileIDs {
		out.Created = append(out.Created, contracts.ConfigurationCreatedProfile{SourceProfileID: in.Selection.ProfileIDs[i], ProfileID: id})
	}
	if !ValidConfigurationImportReceipt(out, in) {
		return contracts.ConfigurationImportReceipt{}, ErrUnavailable
	}
	return out, nil
}

// importReplay reads a committed receipt before any inventory access, allowing exact recovery after source changes.
func (m *Manager) importReplay(ctx context.Context, r ReplayRequest, in contracts.ConfigurationImportRequest) (contracts.ConfigurationImportReceipt, bool, error) {
	var out contracts.ConfigurationImportReceipt
	tx, e := m.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, false, e
	}
	defer tx.Rollback()
	raw, found, e := LookupReplay(tx, r)
	if e != nil || !found {
		return out, found, e
	}
	out, e = decodeImportReceipt(raw, in)
	if e != nil {
		return contracts.ConfigurationImportReceipt{}, false, ErrUnavailable
	}
	return out, true, nil
}

// ApplyConfigurationImport commits new inactive profiles, selected settings and compact replay together.
// No public mutation setter is called, so a late failure rolls back every selected change.
func (m *Manager) ApplyConfigurationImport(parent context.Context, in contracts.ConfigurationImportRequest, principal, key string) (result contracts.ConfigurationImportReceipt, err error) {
	defer func() { err = importError(err) }()
	var zero contracts.ConfigurationImportReceipt
	if !validPrincipal(principal) || ValidateConfigurationImport(in, true) != nil {
		return zero, ErrInvalid
	}
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	raw, _ := json.Marshal(struct {
		IntentDigest string `json:"intentDigest"`
	}{configurationDigest(in)})
	replay := ReplayRequest{Principal: principal, Operation: "configuration.import", Key: key, Payload: string(raw)}
	if replay.Validate() != nil {
		return zero, ErrInvalid
	}
	if out, found, e := m.importReplay(ctx, replay, in); e != nil {
		return zero, e
	} else if found {
		return out, nil
	}
	s, e := m.catalog(ctx, profileSources{Version: 1, Mode: "native_inventory"})
	if e != nil || s.Mode != "native_inventory" {
		return zero, ErrUnavailable
	}
	if importCatalogDigest(s) != in.Expected.CatalogDigest {
		return zero, ErrImportConflict
	}
	preview, planned, e := importPreview(in, s, contracts.ConfigurationCurrentSettings{}, *in.Expected)
	if e != nil {
		return zero, e
	}
	if !preview.CanApply {
		return zero, ErrImportUnmapped
	}
	prepared := []preparedImportProfile{}
	// New technical IDs never reuse a source reference, even when importing into the original NAS.
	sources := map[string]bool{}
	for _, p := range in.Bundle.Profiles {
		sources[p.SourceProfileID] = true
	}
	for _, source := range in.Selection.ProfileIDs {
		p := planned[source]
		p.ID, e = profiles.NewID()
		if e != nil {
			return zero, e
		}
		if sources[p.ID] {
			return zero, ErrUnavailable
		}
		captured, e := m.prepareImportProfile(ctx, p, s)
		if e != nil {
			return zero, e
		}
		prepared = append(prepared, captured)
	}
	fresh, e := m.catalog(ctx, s)
	if e != nil {
		return zero, ErrUnavailable
	}
	if importCatalogDigest(fresh) != in.Expected.CatalogDigest {
		return zero, ErrImportConflict
	}
	// Acquire the writer connection without blocking lifecycle readers. Never wait
	// for the lifecycle lock while holding that connection: older writers may own it.
	conn, e := m.db.Conn(ctx)
	if e != nil {
		return zero, e
	}
	defer conn.Close()
	if !m.mu.TryLock() {
		return zero, ErrUnavailable
	}
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return zero, ErrUnavailable
	}
	tx, e := conn.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback()
	previous, found, e := LookupReplay(tx, replay)
	if e != nil {
		return zero, e
	}
	if found {
		zero, e = decodeImportReceipt(previous, in)
		if e != nil {
			return contracts.ConfigurationImportReceipt{}, ErrUnavailable
		}
		return zero, nil
	}
	_, current, e := importReadState(ctx, tx, principal)
	if e != nil {
		return zero, e
	}
	if !importExpectationMatches(*in.Expected, current, in.Selection) || current.ProfileCount+len(prepared) > 200 {
		return zero, ErrImportConflict
	}
	if in.Selection.Settings.FileTypeMapping {
		var n int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM scan_queue`).Scan(&n); e != nil {
			return zero, e
		}
		if m.active != nil || n > 0 {
			return zero, ErrImportConflict
		}
	}
	out := contracts.ConfigurationImportReceipt{BundleDigest: preview.BundleDigest, SelectionDigest: preview.SelectionDigest, Created: []contracts.ConfigurationCreatedProfile{}, SettingsApplied: in.Selection.Settings, TasksRequireReview: true}
	for i, p := range prepared {
		created, e := persistImportProfile(ctx, tx, p)
		if e != nil {
			return zero, e
		}
		out.Created = append(out.Created, contracts.ConfigurationCreatedProfile{SourceProfileID: in.Selection.ProfileIDs[i], ProfileID: created.ID})
	}
	if e = applyImportSettings(ctx, tx, in, principal, out.Created); e != nil {
		return zero, e
	}
	receipt, e := encodeImportReceipt(out)
	if e != nil {
		return zero, e
	}
	if !ValidConfigurationImportReceipt(out, in) {
		return zero, ErrUnavailable
	}
	if e = RecordReplay(tx, replay, receipt); e != nil {
		return zero, e
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return out, nil
}

// applyImportSettings preserves unrelated groups, LastView and principal while incrementing local revisions.
func applyImportSettings(ctx context.Context, tx *sql.Tx, in contracts.ConfigurationImportRequest, principal string, created []contracts.ConfigurationCreatedProfile) error {
	g := in.Selection.Settings
	if g.Appearance || g.Display || g.PreferredProfile {
		p, e := readPreferences(ctx, tx, principal)
		if e != nil {
			return e
		}
		if p.Revision >= MaxPreferenceRevision {
			return ErrImportConflict
		}
		if g.Appearance {
			a := in.Bundle.Personal.Appearance
			p.Language = a.Language
			p.Theme = a.Theme
			p.Map = a.Map
			p.Locale = a.Locale
			p.TimeZone = a.TimeZone
		}
		if g.Display {
			p.Display = in.Bundle.Personal.Display
		}
		if g.PreferredProfile {
			p.PreferredProfileID = nil
			if source := in.Bundle.Personal.PreferredProfileRef; source != nil {
				for _, c := range created {
					if c.SourceProfileID == *source {
						id := c.ProfileID
						p.PreferredProfileID = &id
					}
				}
				if p.PreferredProfileID == nil {
					return ErrImportUnmapped
				}
			}
		}
		p.Revision++
		if !ValidPreference(p, principal) {
			return ErrInvalid
		}
		raw, _ := json.Marshal(p)
		if _, e = tx.ExecContext(ctx, `INSERT INTO user_preferences(principal,revision,value_json) VALUES(?,?,?) ON CONFLICT(principal) DO UPDATE SET revision=excluded.revision,value_json=excluded.value_json`, principal, p.Revision, string(raw)); e != nil {
			return e
		}
	}
	if g.ExclusionTemplate {
		old, e := readExclusionTemplate(ctx, tx)
		if e != nil {
			return e
		}
		next := ExclusionTemplate{Version: 1, Revision: old.Revision + 1, Rules: in.Bundle.Global.ExclusionTemplate.Rules}
		if !ValidExclusionTemplate(next) {
			return ErrImportConflict
		}
		raw, _ := json.Marshal(next)
		if _, e = tx.ExecContext(ctx, `UPDATE exclusion_template SET revision=?,value_json=? WHERE singleton=1`, next.Revision, string(raw)); e != nil {
			return e
		}
	}
	if g.Retention {
		if _, e := tx.ExecContext(ctx, `UPDATE retention_settings SET complete_count=? WHERE singleton=1`, in.Bundle.Global.RetentionCompleteCount); e != nil {
			return e
		}
	}
	if g.FileTypeMapping {
		old, e := readFileTypeMapping(ctx, tx)
		if e != nil {
			return e
		}
		next := in.Bundle.Global.FileTypeMapping
		next.Revision = old.Revision + 1
		next, e = index.CanonicalFileTypeMapping(next)
		if e != nil {
			return ErrImportConflict
		}
		raw, _ := json.Marshal(next)
		if _, e = tx.ExecContext(ctx, `UPDATE file_type_mapping SET mapping_json=? WHERE singleton=1`, string(raw)); e != nil {
			return e
		}
	}
	return nil
}

// importError preserves public conflict classes while normalizing database/cancellation failures.
func importError(e error) error {
	if e == nil || errors.Is(e, ErrInvalid) || errors.Is(e, ErrImportConflict) || errors.Is(e, ErrImportUnmapped) || errors.Is(e, ErrIdempotencyConflict) || errors.Is(e, ErrConfigurationTooLarge) {
		return e
	}
	return ErrUnavailable
}
