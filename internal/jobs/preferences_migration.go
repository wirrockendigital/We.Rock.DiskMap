package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// migratePreferenceDisplay runs only against an offline candidate. All personal rows,
// historical receipts, and the version marker commit together or remain unchanged.
func migratePreferenceDisplay(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	var step string
	if tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil || version != 6 || tx.QueryRowContext(ctx, "SELECT name FROM schema_steps WHERE version=6").Scan(&step) != nil || step != "onboarding-draft-v1" {
		return migration.ErrVersion
	}
	if err = migratePreferenceRows(ctx, tx, false); err != nil {
		return err
	}
	if err = migratePreferenceRows(ctx, tx, true); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO schema_steps VALUES(7,'personal-display-v2'); PRAGMA user_version=7"); err != nil {
		return err
	}
	return tx.Commit()
}

// Each batch closes its cursor before writes and bounds both row count and JSON bytes.
// The initial page deliberately has no signed-rowid lower bound.
func migratePreferenceRows(ctx context.Context, tx *sql.Tx, receipts bool) error {
	type row struct {
		id           int64
		principal    string
		revision     int
		payload, raw string
	}
	var cursor int64
	first := true
	for {
		query := `SELECT rowid,principal,revision,'',CASE WHEN length(CAST(value_json AS BLOB))<=16384 THEN value_json ELSE '' END FROM user_preferences WHERE (? OR rowid>?) ORDER BY rowid LIMIT 64`
		if receipts {
			query = `SELECT rowid,principal,0,CASE WHEN length(CAST(payload AS BLOB))<=16384 THEN payload ELSE '' END,CASE WHEN length(CAST(result_json AS BLOB))<=16384 THEN result_json ELSE '' END FROM idempotency WHERE operation='preferences.update' AND (? OR rowid>?) ORDER BY rowid LIMIT 64`
		}
		rows, err := tx.QueryContext(ctx, query, first, cursor)
		if err != nil {
			return err
		}
		batch := make([]row, 0, 64)
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.id, &r.principal, &r.revision, &r.payload, &r.raw); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(batch) == 0 {
			return nil
		}
		for _, r := range batch {
			raw, value, err := upgradePreferenceV1(r.raw, r.principal)
			if err != nil {
				return err
			}
			if receipts {
				var request PreferenceRequest
				if security.DecodeJSON(strings.NewReader(r.payload), 16384, &request) != nil || request.ContractVersion != 1 || request.Display != nil {
					return ErrUnavailable
				}
				// Version conversion here validates historical intent only; stored payload bytes stay exact.
				request.ContractVersion = contracts.PreferenceVersion
				if request.Validate() != nil || value.Revision != request.ExpectedRevision+1 || !preferenceMatchesGroup(value, request) {
					return ErrUnavailable
				}
				_, err = tx.ExecContext(ctx, "UPDATE idempotency SET result_json=? WHERE rowid=? AND principal=? AND operation='preferences.update' AND payload=? AND result_json=?", raw, r.id, r.principal, r.payload, r.raw)
			} else {
				if r.revision < 1 || value.Revision != r.revision {
					return ErrUnavailable
				}
				_, err = tx.ExecContext(ctx, "UPDATE user_preferences SET value_json=? WHERE rowid=? AND principal=? AND revision=? AND value_json=?", raw, r.id, r.principal, r.revision, r.raw)
			}
			if err != nil {
				return err
			}
		}
		cursor = batch[len(batch)-1].id
		first = false
	}
}

// upgradePreferenceV1 accepts only the previous DTO, retaining every non-display field.
// It is migration-only; ordinary reads never manufacture fields for existing records.
func upgradePreferenceV1(raw, principal string) (string, contracts.UserPreference, error) {
	var old contracts.UserPreference
	var fields map[string]json.RawMessage
	if security.DecodeJSON(strings.NewReader(raw), 16384, &old) != nil || json.Unmarshal([]byte(raw), &fields) != nil || old.Version != 1 || old.Revision < 1 {
		return "", old, ErrUnavailable
	}
	if _, exists := fields["display"]; exists {
		return "", old, ErrUnavailable
	}
	old.Version = contracts.PreferenceVersion
	old.Display = contracts.DefaultPreferenceDisplay()
	if !ValidPreference(old, principal) {
		return "", old, ErrUnavailable
	}
	fields["version"] = json.RawMessage(`2`)
	fields["display"], _ = json.Marshal(old.Display)
	result, err := json.Marshal(fields)
	if err != nil || len(result) > 16384 {
		return "", old, ErrUnavailable
	}
	return string(result), old, nil
}

// preferenceMatchesGroup qualifies immutable receipts against the exact submitted field group.
func preferenceMatchesGroup(value contracts.UserPreference, in PreferenceRequest) bool {
	switch in.Group {
	case "workspace":
		return in.Workspace != nil && reflect.DeepEqual(in.Workspace, value.Workspace)
	case "appearance":
		return in.Appearance != nil && reflect.DeepEqual(*in.Appearance, contracts.PreferenceAppearance{Language: value.Language, Theme: value.Theme, Map: value.Map, Locale: value.Locale, TimeZone: value.TimeZone})
	case "display":
		return in.Display != nil && *in.Display == value.Display
	case "preferredProfile":
		return in.PreferredProfile != nil && reflect.DeepEqual(in.PreferredProfile.ProfileID, value.PreferredProfileID)
	case "lastView":
		return in.View != nil && reflect.DeepEqual(in.View.Value, value.LastView)
	}
	return false
}
