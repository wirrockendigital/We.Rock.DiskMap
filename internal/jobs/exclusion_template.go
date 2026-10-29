package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

var ErrTemplateConflict = errors.New("TEMPLATE_CONFLICT")

// ExclusionTemplate is a global copy source, never an automatically inherited scan rule.
type ExclusionTemplate struct {
	Version  int                   `json:"version"`
	Revision int                   `json:"revision"`
	Rules    []contracts.Exclusion `json:"rules"`
}

// ExclusionTemplateRequest replaces the complete template under an explicit revision.
type ExclusionTemplateRequest struct {
	ContractVersion  int                   `json:"contractVersion"`
	ExpectedRevision int                   `json:"expectedRevision"`
	Rules            []contracts.Exclusion `json:"rules"`
}

// Validate enforces the same rule language and bounded canonical action bytes as profiles.
func (r ExclusionTemplateRequest) Validate() error {
	if r.ContractVersion != 1 || r.ExpectedRevision < 0 || r.ExpectedRevision >= MaxPreferenceRevision || r.Rules == nil {
		return ErrInvalid
	}
	if _, e := profiles.EffectiveExclusions(nil, r.Rules); e != nil {
		return ErrInvalid
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 16384 {
		return ErrInvalid
	}
	return nil
}

// ValidExclusionTemplate also rejects noncanonical persisted or replayed rule lists.
func ValidExclusionTemplate(v ExclusionTemplate) bool {
	if v.Version != 1 || v.Revision < 0 || v.Revision > MaxPreferenceRevision || v.Rules == nil {
		return false
	}
	rules, e := profiles.EffectiveExclusions(nil, v.Rules)
	if e != nil || !reflect.DeepEqual(rules, v.Rules) || v.Revision == 0 && len(v.Rules) != 0 {
		return false
	}
	raw, e := json.Marshal(v)
	return e == nil && len(raw) <= 16384
}

// Missing singleton state is corruption, not an implicit reset to revision zero.
func readExclusionTemplate(ctx context.Context, db preferenceReader) (ExclusionTemplate, error) {
	var v ExclusionTemplate
	var raw string
	var revision int
	if e := db.QueryRowContext(ctx, "SELECT revision,value_json FROM exclusion_template WHERE singleton=1").Scan(&revision, &raw); e != nil {
		return v, ErrUnavailable
	}
	if security.DecodeJSON(strings.NewReader(raw), 16384, &v) != nil || !ValidExclusionTemplate(v) || v.Revision != revision {
		return ExclusionTemplate{}, ErrUnavailable
	}
	return v, nil
}

// ExclusionTemplate reads shared state only for a trusted authenticated principal.
func (m *Manager) ExclusionTemplate(ctx context.Context, principal string) (ExclusionTemplate, error) {
	if !validPrincipal(principal) {
		return ExclusionTemplate{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ExclusionTemplate{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return readExclusionTemplate(ctx, m.db)
}

// SetExclusionTemplate commits the revision and principal-bound receipt atomically.
func (m *Manager) SetExclusionTemplate(ctx context.Context, in ExclusionTemplateRequest, principal, key string) (ExclusionTemplate, error) {
	var zero ExclusionTemplate
	if in.Validate() != nil || !validPrincipal(principal) {
		return zero, ErrInvalid
	}
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{principal, "exclusion-template.update", key, string(raw)}
	if replay.Validate() != nil {
		return zero, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return zero, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, e := m.db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback()
	previous, found, e := LookupReplay(tx, replay)
	if e != nil {
		return zero, e
	}
	if found {
		if security.DecodeJSON(strings.NewReader(string(previous)), 16384, &zero) != nil || !ValidExclusionTemplate(zero) || zero.Revision != in.ExpectedRevision+1 {
			return ExclusionTemplate{}, ErrUnavailable
		}
		rules, _ := profiles.EffectiveExclusions(nil, in.Rules)
		if !reflect.DeepEqual(rules, zero.Rules) {
			return ExclusionTemplate{}, ErrUnavailable
		}
		return zero, nil
	}
	old, e := readExclusionTemplate(ctx, tx)
	if e != nil {
		return zero, e
	}
	if old.Revision != in.ExpectedRevision {
		return zero, ErrTemplateConflict
	}
	rules, _ := profiles.EffectiveExclusions(nil, in.Rules)
	out := ExclusionTemplate{1, old.Revision + 1, rules}
	if !ValidExclusionTemplate(out) {
		return zero, ErrInvalid
	}
	value, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, "UPDATE exclusion_template SET revision=?,value_json=? WHERE singleton=1", out.Revision, string(value)); e != nil {
		return zero, e
	}
	if e = RecordReplay(tx, replay, value); e != nil {
		return zero, e
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return out, nil
}

// migrateExclusionTemplate changes only a fresh database or an offline upgrade candidate.
func migrateExclusionTemplate(ctx context.Context, db *sql.DB) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var name string
	if tx.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=4").Scan(&name) != nil || name != "personal-preferences-v1" {
		return ErrUnavailable
	}
	_, e = tx.ExecContext(ctx, `CREATE TABLE exclusion_template(singleton INTEGER PRIMARY KEY CHECK(singleton=1),revision INTEGER NOT NULL CHECK(revision BETWEEN 0 AND 2147483647),value_json TEXT NOT NULL CHECK(length(CAST(value_json AS BLOB))<=16384)); INSERT INTO exclusion_template VALUES(1,0,'{"version":1,"revision":0,"rules":[]}'); INSERT INTO schema_steps VALUES(5,'exclusion-template-v1'); PRAGMA user_version=5`)
	if e != nil {
		return e
	}
	return tx.Commit()
}
