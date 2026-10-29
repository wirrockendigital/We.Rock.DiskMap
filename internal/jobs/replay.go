package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// ReplayRequest binds one canonical action to the authenticated principal and operation namespace.
type ReplayRequest struct{ Principal, Operation, Key, Payload string }

// Validate limits persisted replay material and forbids unknown operation namespaces.
func (r ReplayRequest) Validate() error {
	limit := 16384
	if r.Operation == "onboarding.update" {
		limit = 32768
	}
	if !validPrincipal(r.Principal) || !identifier.MatchString(r.Key) || len(r.Payload) > limit || !json.Valid([]byte(r.Payload)) {
		return ErrInvalid
	}
	switch r.Operation {
	case "index.reset", "configuration.import", "onboarding.update", "exclusion-template.update", "preferences.update", "index.check", "index.delete", "storage.retention", "scan.start", "job.pause", "job.resume", "job.cancel", "job.cancel-queued", "profile.create", "profile.update", "profile.duplicate", "profile.deactivate", "profile.delete":
		return nil
	}
	return ErrInvalid
}

// LookupReplay runs inside the same owner transaction that will perform the domain change.
func LookupReplay(tx *sql.Tx, r ReplayRequest) (json.RawMessage, bool, error) {
	if err := r.Validate(); err != nil {
		return nil, false, err
	}
	var previous, result string
	err := tx.QueryRow(`SELECT payload,result_json FROM idempotency WHERE principal=? AND operation=? AND key=?`, r.Principal, r.Operation, r.Key).Scan(&previous, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if previous != r.Payload {
		return nil, false, ErrIdempotencyConflict
	}
	if len(result) > 65536 || !json.Valid([]byte(result)) {
		return nil, false, ErrUnavailable
	}
	return json.RawMessage(result), true, nil
}

// RecordReplay atomically binds a domain result before the owner commits; it never commits alone.
func RecordReplay(tx *sql.Tx, r ReplayRequest, result json.RawMessage) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if len(result) > 65536 || !json.Valid(result) {
		return ErrInvalid
	}
	_, err := tx.Exec(`INSERT INTO idempotency(principal,operation,key,payload,result_json) VALUES(?,?,?,?,?)`, r.Principal, r.Operation, r.Key, r.Payload, string(result))
	return err
}

// CommitReplay launches post-commit work only after durable domain and replay state succeeded.
func CommitReplay(tx *sql.Tx, after func()) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	if after != nil {
		after()
	}
	return nil
}

// migrateReplaySchema preserves pre-slice scan keys in one transaction, then uses only result_json.
func migrateReplaySchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version != 0 && version != 2 {
		return ErrUnavailable
	}
	rows, err := tx.Query(`PRAGMA table_info(idempotency)`)
	if err != nil {
		return err
	}
	old, current := false, false
	for rows.Next() {
		var number, notnull, pk int
		var name, kind string
		var def any
		if err = rows.Scan(&number, &name, &kind, &notnull, &def, &pk); err != nil {
			rows.Close()
			return err
		}
		old = old || name == "job_id"
		current = current || name == "result_json"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if old == current || (version == 2 && old) {
		return ErrUnavailable
	}
	if old {
		if _, err = tx.Exec(`ALTER TABLE idempotency RENAME COLUMN job_id TO result_json`); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE idempotency SET result_json=json_object('jobId',result_json)`); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`PRAGMA user_version=2`); err != nil {
		return err
	}
	return tx.Commit()
}
