package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// generationFixture uses exact values beyond binary64 precision and distinct unknown/zero nodes.
func generationFixture(t *testing.T) (*sql.DB, GenerationHeader) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = InitializeGeneration(context.Background(), db, "snapshot"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewNodeWriter(context.Background(), tx, "snapshot")
	if err != nil {
		t.Fatal(err)
	}
	root, err := w.AddRoot("volume", "volume", nil)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := w.Insert(root, 0, []byte("."), "directory", nil, nil, "private-identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"large", "zero", "unknown"} {
		var size *contracts.Bytes
		if rel != "unknown" {
			v := contracts.Bytes("0")
			if rel == "large" {
				v = "9007199254740993"
			}
			size = &v
		}
		if _, err = w.Insert(root, parent, []byte(rel), "file", size, nil, "private-identity", nil); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); err != nil {
		t.Fatal(err)
	}
	if err := BuildDerivedProjections(context.Background(), db, DefaultFileTypeMapping()); err != nil {
		t.Fatal(err)
	}
	h := GenerationHeader{AggregateVersion: AggregateVersion, FileTypeMapping: DefaultFileTypeMapping(), SnapshotID: "snapshot", JobID: "job", Profile: contracts.Profile{ID: "profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-09T00:00:00Z", EndedAt: "2026-10-09T00:01:00Z", ScannerVersion: ScannerVersion, SchemaVersion: GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: 4, LogicalBytes: "9007199254740993"}
	h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
	return db, h
}

// TestGenerationRoundtripAndSeal checks lossless durable data and immutable finalized rows.
func TestGenerationRoundtripAndSeal(t *testing.T) {
	db, h := generationFixture(t)
	ctx := context.Background()
	if err := FinalizeGeneration(ctx, db, h); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGenerationHeader(ctx, db, "snapshot")
	if err != nil || got.LogicalBytes != h.LogicalBytes || got.UnknownLogicalEntries != 1 || len(got.Coverage) != 1 {
		t.Fatal(got, err)
	}
	for _, query := range []string{`DELETE FROM nodes`, `UPDATE roots SET target_id='wrong'`, `DELETE FROM coverage`, `UPDATE generation_identity SET snapshot_id='new'`, `DELETE FROM generation_header`, `UPDATE generation_header SET header_json='{}'`} {
		if _, err = db.Exec(query); err == nil {
			t.Fatal("sealed write accepted", query)
		}
	}
	if FinalizeGeneration(ctx, db, h) == nil {
		t.Fatal("second seal accepted")
	}
	if _, err = ReadGenerationHeader(ctx, db, "foreign"); err == nil {
		t.Fatal("wrong snapshot accepted")
	}
}

// TestGenerationInvalidGraph rejects corrupt SQL/JSON bindings without publishing a header.
func TestGenerationInvalidGraph(t *testing.T) {
	cases := map[string]string{
		"foreignSnapshot":       `UPDATE generation_identity SET snapshot_id='other'`,
		"orphan":                `UPDATE nodes SET parent_id=999 WHERE id=2`,
		"cycle":                 `UPDATE nodes SET parent_id=id WHERE id=2`,
		"sqlJSONIdentity":       `UPDATE nodes SET id=0 WHERE id=2`,
		"foreignTarget":         `UPDATE roots SET target_id='other'`,
		"numericBytes":          `UPDATE nodes SET logical_bytes=CAST('9007199254740993' AS BLOB) WHERE id=2`,
		"signedBytes":           `UPDATE nodes SET logical_bytes='-1' WHERE id=2`,
		"noncanonicalAllocated": `UPDATE nodes SET allocated_bytes='01' WHERE id=2`,
		"blobAllocated":         `UPDATE nodes SET allocated_bytes=CAST('12' AS BLOB) WHERE id=2`,
		"fractionalLinks":       `UPDATE nodes SET link_count=1.25 WHERE id=2`,
		"numericName":           `UPDATE nodes SET name_bytes=123 WHERE id=2`,

		"unknownSchema":      `PRAGMA user_version=99`,
		"nonDirectoryParent": `UPDATE nodes SET kind='file' WHERE parent_id IS NULL`,
		"missingRoot":        `DELETE FROM nodes`,
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			db, h := generationFixture(t)
			// Simulate storage corruption beyond the normal writer constraints.
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF;PRAGMA ignore_check_constraints=ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if FinalizeGeneration(context.Background(), db, h) == nil {
				t.Fatal("invalid generation sealed")
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM generation_header`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}

// TestGenerationForeignKeyAndDuplicate proves SQL constraints as well as finalizer checks.
func TestGenerationForeignKeyAndDuplicate(t *testing.T) {
	db, _ := generationFixture(t)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE nodes SET parent_id='missing' WHERE parent_id IS NOT NULL`); err == nil {
		t.Fatal("orphan committed")
	}
	if _, err := db.Exec(`INSERT INTO nodes SELECT * FROM nodes LIMIT 1`); err == nil {
		t.Fatal("duplicate committed")
	}
}

// TestGenerationPartialPreservesGaps distinguishes empty unavailable roots from complete roots.
func TestGenerationPartialPreservesGaps(t *testing.T) {
	db, h := generationFixture(t)
	if _, err := db.Exec(`DELETE FROM directory_aggregates; DELETE FROM nodes; UPDATE coverage SET state='partial',errors=1`); err != nil {
		t.Fatal(err)
	}
	h.State = "partial"
	h.CoverageErrors = 1
	h.Entries = 0
	h.LogicalBytes = "0"
	if err := FinalizeGeneration(context.Background(), db, h); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGenerationHeader(context.Background(), db, h.SnapshotID)
	if err != nil || got.State != "partial" || got.Coverage[0].Errors != 1 {
		t.Fatal(got, err)
	}
}

// TestEndValidationEvidenceSealsSeparately preserves the exact start manifest with later source gaps.
func TestEndValidationEvidenceSealsSeparately(t *testing.T) {
	db, h := generationFixture(t)
	start, _ := json.Marshal(h.Manifest)
	h.State = "partial"
	h.CoverageErrors = 1
	h.EndValidation = []EndValidationGap{{TargetID: "volume", SourceID: "share-a", Code: "SCOPE_CHANGED"}}
	if _, err := db.Exec(`UPDATE coverage SET state='partial',errors=1`); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeGeneration(context.Background(), db, h); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGenerationHeader(context.Background(), db, h.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	end, _ := json.Marshal(got.Manifest)
	if string(start) != string(end) || len(got.EndValidation) != 1 || got.EndValidation[0].SourceID != "share-a" {
		t.Fatal("start/end evidence mixed")
	}
}

// TestInvalidEndValidation rejects incomplete or unrelated end-gap claims before sealing.
func TestInvalidEndValidation(t *testing.T) {
	for _, scenario := range []string{"target", "source", "code", "complete", "coverage"} {
		t.Run(scenario, func(t *testing.T) {
			db, h := generationFixture(t)
			h.State = "partial"
			h.CoverageErrors = 1
			h.EndValidation = []EndValidationGap{{TargetID: "volume", SourceID: "share-a", Code: "SCOPE_CHANGED"}}
			if scenario != "coverage" {
				if _, err := db.Exec(`UPDATE coverage SET state='partial',errors=1`); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "target":
				h.EndValidation[0].TargetID = "outside"
			case "source":
				h.EndValidation[0].SourceID = "/private/path"
			case "code":
				h.EndValidation[0].Code = "arbitrary detail"
			case "complete":
				h.State = "complete"
				h.CoverageErrors = 0
			}
			if FinalizeGeneration(context.Background(), db, h) == nil {
				t.Fatal("invalid end evidence accepted")
			}
		})
	}
}

// Finalization accepts a terminal leaf below 256 directories but rejects a further directory.
func TestGenerationDepthBoundary(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			db, h := generationFixture(t)
			ctx := context.Background()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			writer, err := NewNodeWriter(ctx, tx, "snapshot")
			if err != nil {
				t.Fatal(err)
			}
			parent := int64(1)
			for i := 0; i < 256; i++ {
				parent, err = writer.Insert(1, parent, []byte("d"), "directory", nil, nil, "identity", nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = writer.Insert(1, parent, []byte("terminal"), kind, nil, nil, "identity", nil); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			h.Entries += 257
			if _, err = db.Exec(`DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping`); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				if err = BuildDerivedProjections(ctx, db, h.FileTypeMapping); err != nil {
					t.Fatal(err)
				}
			}
			err = FinalizeGeneration(ctx, db, h)
			if kind == "file" && err != nil {
				t.Fatal(err)
			}
			if kind == "directory" && err == nil {
				t.Fatal("excessive directory depth accepted")
			}
		})
	}
}
