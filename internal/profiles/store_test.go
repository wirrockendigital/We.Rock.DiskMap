package profiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// storeFixture creates the production table shape without inventing filesystem paths.
func storeFixture(t *testing.T) (*sql.DB, Record, map[string]bool) {
	t.Helper()
	db, err := index.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = Initialize(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE jobs(id TEXT PRIMARY KEY,state TEXT,outcome_json TEXT);CREATE TABLE active_generations(profile_id TEXT PRIMARY KEY,snapshot_id TEXT);CREATE TABLE profile_sources(profile_id TEXT PRIMARY KEY,sources_json TEXT);`); err != nil {
		t.Fatal(err)
	}
	io := contracts.Bytes("9007199254740993")
	cpu := 25
	return db, Record{Profile: contracts.Profile{ID: "stable-profile", Revision: 1, Name: "Medien – 東京 & 'Backup'", Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}, Exclusions: []contracts.Exclusion{{Kind: "pattern", Value: "*.tmp"}}, Active: true, ResourceMode: "custom", Limits: contracts.ResourceLimits{Workers: 2, IOBytesPerSecond: &io, CPUPercent: &cpu}, FreshnessSeconds: 3600}, RootsJSON: `{}`, VolumesJSON: `{}`}, map[string]bool{"share-a": true}
}

// writeRecord commits through the same transaction-scoped API used by domain/replay integration.
func writeRecord(t *testing.T, db *sql.DB, r Record, rev int, known map[string]bool) contracts.Profile {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	p, err := Write(context.Background(), tx, r, rev, known)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestProfileRoundtripRenameVersionDelete proves exact options, immutable IDs and retired identities.
func TestProfileRoundtripRenameVersionDelete(t *testing.T) {
	db, r, known := storeFixture(t)
	p := writeRecord(t, db, r, 0, known)
	tx, _ := db.Begin()
	got, err := Read(context.Background(), tx, p.ID)
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(r.Profile)
	b, _ := json.Marshal(got.Profile)
	if string(a) != string(b) {
		t.Fatal("profile fields lost")
	}
	got.Profile.Name = "Neuer Name 🗂"
	got.Profile.Revision = 999
	renamed := writeRecord(t, db, got, 1, known)
	if renamed.ID != p.ID || renamed.Revision != 1 {
		t.Fatal(renamed)
	}
	got.Profile = renamed
	got.Profile.Exclusions = append(got.Profile.Exclusions, contracts.Exclusion{Kind: "path", Value: "temp"})
	changed := writeRecord(t, db, got, 1, known)
	if changed.Revision != 2 {
		t.Fatal(changed)
	}
	tx, _ = db.Begin()
	if err = DeleteUnused(context.Background(), tx, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin()
	defer tx.Rollback()
	if _, err = Read(context.Background(), tx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = Write(context.Background(), tx, r, 0, known); !errors.Is(err, ErrRetired) {
		t.Fatal("retired ID reused", err)
	}
}

// TestProfileUnknownRootAndConflict rejects invented source identities and stale writers.
func TestProfileUnknownRootAndConflict(t *testing.T) {
	db, r, known := storeFixture(t)
	writeRecord(t, db, r, 0, known)
	for _, tc := range []struct {
		name   string
		modify func(*Record)
		rev    int
		want   error
	}{
		{"unknown", func(r *Record) { r.Profile.Scope.TargetIDs = []string{"invented"} }, 1, ErrInvalid},
		{"stale", func(r *Record) {}, 2, ErrConflict},
		{"badname", func(r *Record) { r.Profile.Name = "\n" }, 1, ErrInvalid},
		{"badexclusion", func(r *Record) { r.Profile.Exclusions[0].Kind = "arbitrary" }, 1, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := r
			copy.Profile.Exclusions = append([]contracts.Exclusion{}, r.Profile.Exclusions...)
			tc.modify(&copy)
			tx, _ := db.Begin()
			defer tx.Rollback()
			if _, err := Write(context.Background(), tx, copy, tc.rev, known); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
}

// TestProfileReferencesProtectUsedState keeps complete snapshots and historical jobs intact.
func TestProfileReferencesProtectUsedState(t *testing.T) {
	db, r, known := storeFixture(t)
	writeRecord(t, db, r, 0, known)
	if _, err := db.Exec(`INSERT INTO jobs VALUES('job-old','completed','{"job":{"profileId":"stable-profile"}}');INSERT INTO active_generations VALUES('stable-profile','snapshot-old')`); err != nil {
		t.Fatal(err)
	}
	summaries, err := List(context.Background(), db)
	if err != nil || len(summaries) != 1 || *summaries[0].LatestJobID != "job-old" || *summaries[0].ActiveSnapshotID != "snapshot-old" {
		t.Fatal(summaries, err)
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	if err = DeleteUnused(context.Background(), tx, r.Profile.ID, 1); !errors.Is(err, ErrInUse) {
		t.Fatal(err)
	}
}
