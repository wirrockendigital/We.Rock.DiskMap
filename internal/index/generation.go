package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// GenerationSchema is independent from the mutable control database schema.
const GenerationSchema = 13

// ScannerVersion names the qualified metadata and boundary semantics.
const ScannerVersion = "7"

// ErrGeneration deliberately contains no source paths or private metadata.
var ErrGeneration = errors.New("INVALID_GENERATION")

// CoverageRecord preserves measured gaps rather than converting them to empty trees.
type CoverageRecord struct {
	TargetID string `json:"targetId"`
	State    string `json:"state"`
	Errors   uint64 `json:"errors"`
}

// EndValidationGap is post-traversal evidence, separate from the immutable start manifest.
type EndValidationGap struct {
	TargetID string `json:"targetId"`
	SourceID string `json:"sourceId"`
	Code     string `json:"code"`
}

// GenerationHeader captures the immutable inputs, methods and measured result of one scan.
// Physical bindings are private package data and must never be served as public DTOs.
type GenerationHeader struct {
	AggregateVersion      int                         `json:"aggregateVersion"`
	FileTypeMapping       contracts.FileTypeMapping   `json:"fileTypeMapping"`
	ScanBoundaryCount     contracts.Bytes             `json:"scanBoundaryCount"`
	EndValidation         []EndValidationGap          `json:"endValidation"`
	SnapshotID            string                      `json:"snapshotId"`
	JobID                 string                      `json:"jobId"`
	Profile               contracts.Profile           `json:"profile"`
	Manifest              scopemanifest.ScopeManifest `json:"manifest"`
	StartedAt             string                      `json:"startedAt"`
	EndedAt               string                      `json:"endedAt"`
	ScannerVersion        string                      `json:"scannerVersion"`
	SchemaVersion         int                         `json:"schemaVersion"`
	Methods               []string                    `json:"methods"`
	State                 string                      `json:"state"`
	Entries               uint64                      `json:"entries"`
	LogicalBytes          contracts.Bytes             `json:"logicalBytes"`
	UnknownLogicalEntries uint64                      `json:"unknownLogicalEntries"`
	CoverageErrors        uint64                      `json:"coverageErrors"`
	Coverage              []CoverageRecord            `json:"coverage"`
}

// InitializeGeneration creates only a fresh generation; unknown and old schemas are not migrated.
func InitializeGeneration(ctx context.Context, db *sql.DB, snapshot string) error {
	if !security.ValidID(snapshot) {
		return ErrGeneration
	}
	// Foreign keys protect the production writer; the streaming pass independently checks ancestry.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `PRAGMA user_version=`+strconv.Itoa(GenerationSchema)+`;
 CREATE TABLE generation_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),snapshot_id TEXT NOT NULL UNIQUE);
 CREATE TABLE generation_header(singleton INTEGER PRIMARY KEY CHECK(singleton=1),header_json TEXT NOT NULL CHECK(json_valid(header_json)));
 CREATE TABLE roots(id INTEGER PRIMARY KEY CHECK(id>0),target_id TEXT NOT NULL UNIQUE,volume_id TEXT NOT NULL,share_id TEXT);
 CREATE TABLE nodes(id INTEGER PRIMARY KEY CHECK(id>0),parent_id INTEGER,root_id INTEGER NOT NULL,name_bytes BLOB NOT NULL,kind TEXT NOT NULL,logical_bytes TEXT,allocated_bytes TEXT,object_identity TEXT NOT NULL,link_count INTEGER,CHECK(parent_id IS NULL OR (parent_id>0 AND parent_id<id)),FOREIGN KEY(parent_id) REFERENCES nodes(id),FOREIGN KEY(root_id) REFERENCES roots(id));
 CREATE INDEX nodes_parent ON nodes(parent_id,id);
 CREATE UNIQUE INDEX nodes_name ON nodes(parent_id,name_bytes) WHERE parent_id IS NOT NULL;
 CREATE UNIQUE INDEX nodes_root ON nodes(root_id) WHERE parent_id IS NULL;
 CREATE TABLE directory_aggregates(node_id INTEGER PRIMARY KEY NOT NULL,aggregate_json TEXT NOT NULL CHECK(json_valid(aggregate_json)),FOREIGN KEY(node_id) REFERENCES nodes(id));
 CREATE TABLE aggregate_mapping(singleton INTEGER PRIMARY KEY CHECK(singleton=1),mapping_json TEXT NOT NULL CHECK(json_valid(mapping_json)));
 CREATE TABLE scan_boundaries(target_id TEXT NOT NULL,path_bytes BLOB NOT NULL,reason_code TEXT NOT NULL,PRIMARY KEY(target_id,path_bytes,reason_code));
 CREATE TABLE coverage(target_id TEXT PRIMARY KEY NOT NULL,state TEXT NOT NULL CHECK(state IN ('complete','partial')),errors INTEGER NOT NULL CHECK(errors>=0));`)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO generation_identity VALUES(1,?)`, snapshot); err != nil {
		return err
	}
	// Sealing is enforced even when a caller accidentally opens the published database writable.
	for _, table := range []string{"roots", "nodes", "coverage", "scan_boundaries", "generation_identity", "directory_aggregates", "aggregate_mapping"} {
		for _, action := range []string{"INSERT", "UPDATE", "DELETE"} {
			if _, err = tx.ExecContext(ctx, `CREATE TRIGGER seal_`+table+`_`+action+` BEFORE `+action+` ON `+table+` WHEN EXISTS(SELECT 1 FROM generation_header) BEGIN SELECT RAISE(ABORT,'GENERATION_SEALED'); END`); err != nil {
				return err
			}
		}
	}
	for _, action := range []string{"UPDATE", "DELETE"} {
		if _, err = tx.ExecContext(ctx, `CREATE TRIGGER seal_header_`+action+` BEFORE `+action+` ON generation_header BEGIN SELECT RAISE(ABORT,'GENERATION_SEALED'); END`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FinalizeGeneration validates in a streaming pass and atomically seals the measured header.
// Memory depends on scope roots and one node, not on the number of indexed files.
func FinalizeGeneration(ctx context.Context, db *sql.DB, h GenerationHeader) error {
	return validateGeneration(ctx, db, h, true)
}

// validateGeneration shares all seal invariants with explicit read-only integrity verification.
func validateGeneration(ctx context.Context, db *sql.DB, h GenerationHeader, seal bool) error {
	original := h
	if h.AggregateVersion != AggregateVersion || h.SchemaVersion != GenerationSchema || h.ScannerVersion != ScannerVersion || !security.ValidID(h.JobID) || !security.ValidID(h.Profile.ID) || h.Profile.Revision < 1 || h.LogicalBytes.Validate() != nil || h.Manifest.ContractVersion != scopemanifest.ScopeManifestVersion || len(h.Manifest.Roots) == 0 {
		return ErrGeneration
	}
	start, e := time.Parse(time.RFC3339Nano, h.StartedAt)
	if e != nil {
		return ErrGeneration
	}
	end, e := time.Parse(time.RFC3339Nano, h.EndedAt)
	if e != nil || end.Before(start) {
		return ErrGeneration
	}
	if (h.State != "complete" && h.State != "partial") || (h.State == "complete") != (h.CoverageErrors == 0) || !validGenerationMethods(h.Methods) {
		return ErrGeneration
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = validateSQLite(ctx, tx); err != nil {
		return err
	}
	var version int
	var snapshot string
	if tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version) != nil || version != GenerationSchema || tx.QueryRowContext(ctx, `SELECT snapshot_id FROM generation_identity WHERE singleton=1`).Scan(&snapshot) != nil || snapshot != h.SnapshotID {
		return ErrGeneration
	}
	roots := map[string]scopemanifest.ManifestRoot{}
	expected := map[string]bool{}
	for _, r := range h.Manifest.Roots {
		if !security.ValidID(r.TargetID) || !security.ValidID(r.VolumeID) || (r.Kind != "share" && r.Kind != "volume") {
			return ErrGeneration
		}
		if _, ok := roots[r.TargetID]; ok {
			return ErrGeneration
		}
		roots[r.TargetID] = r
		expected[r.TargetID] = true
	}
	for _, issue := range h.Manifest.Issues {
		expected[issue.TargetID] = true
	}
	if err := ValidateSourceProvenance(h.Manifest); err != nil {
		return err
	}
	if err := ValidateQualifiedSubvolumes(h.Manifest); err != nil {
		return err
	}
	// Keep validated root bindings once instead of joining and decoding them for every file.
	rootTargets := map[int64]string{}
	// Normalized sources remain checked even when a root was unreadable and has no node rows.
	rootRows, err := tx.QueryContext(ctx, `SELECT id,target_id,volume_id,share_id,typeof(target_id)='text' AND typeof(volume_id)='text' AND typeof(share_id) IN ('null','text') FROM roots`)
	if err != nil {
		return err
	}
	for rootRows.Next() {
		var id int64
		var target, volume string
		var share sql.NullString
		var typed bool
		if err = rootRows.Scan(&id, &target, &volume, &share, &typed); err != nil {
			break
		}
		r, ok := roots[target]
		if !typed || id < 1 || !ok || r.VolumeID != volume || share.Valid != (r.Kind == "share") || share.Valid && share.String != target {
			err = ErrGeneration
			break
		}
		rootTargets[id] = target
	}
	if err == nil {
		err = rootRows.Err()
	}
	rootRows.Close()
	if err != nil {
		return err
	}
	endTargets := map[string]bool{}
	for _, gap := range h.EndValidation {
		if _, ok := roots[gap.TargetID]; !ok || !security.ValidID(gap.SourceID) || (gap.Code != "SCOPE_CHANGED" && gap.Code != "SCOPE_VALIDATION_UNAVAILABLE") || h.State != "partial" || h.CoverageErrors == 0 {
			return ErrGeneration
		}
		endTargets[gap.TargetID] = true
	}
	// Positive ordered locators make cycles impossible; every relation and root is checked independently.
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.parent_id,n.root_id,n.name_bytes,n.kind,n.logical_bytes,n.allocated_bytes,p.kind,p.root_id,
 typeof(n.name_bytes)='blob' AND typeof(n.kind)='text' AND typeof(n.object_identity)='text' AND typeof(n.link_count) IN ('null','integer') AND typeof(n.logical_bytes) IN ('null','text') AND typeof(n.allocated_bytes) IN ('null','text')
 FROM nodes n LEFT JOIN nodes p ON p.id=n.parent_id ORDER BY n.id`)
	if err != nil {
		return err
	}
	rootSeen := map[string]bool{}
	count, unknown, directories := uint64(0), uint64(0), uint64(0)
	sum := new(big.Int)
	// Reuse scan destinations; none of these per-row values escape the validation pass.
	var id, rootID int64
	var parent, parentRoot sql.NullInt64
	var name []byte
	var kind string
	var logical, allocated, parentKind sql.NullString
	var typed bool
	var measured big.Int
	for rows.Next() {
		if err = rows.Scan(&id, &parent, &rootID, &name, &kind, &logical, &allocated, &parentKind, &parentRoot, &typed); err != nil {
			break
		}
		target, ok := rootTargets[rootID]
		node := contracts.Node{Kind: kind}
		if logical.Valid {
			v := contracts.Bytes(logical.String)
			node.LogicalBytes = &v
		}
		if allocated.Valid {
			v := contracts.Bytes(allocated.String)
			node.AllocatedBytes = &v
		}
		if !typed || id < 1 || rootID < 1 || !ok || len(name) == 0 || strings.ContainsAny(string(name), "/\x00") || contracts.ValidateNodeType(node) != nil || logical.Valid && node.LogicalBytes.Validate() != nil || allocated.Valid && node.AllocatedBytes.Validate() != nil || kind == "directory" && (logical.Valid || allocated.Valid) {
			err = ErrGeneration
			break
		}
		if parent.Valid {
			if parent.Int64 < 1 || parent.Int64 >= id || !parentKind.Valid || parentKind.String != "directory" || !parentRoot.Valid || parentRoot.Int64 != rootID || string(name) == "." || string(name) == ".." {
				err = ErrGeneration
				break
			}
		} else {
			if kind != "directory" || rootSeen[target] {
				err = ErrGeneration
				break
			}
			rootSeen[target] = true
		}
		if kind == "directory" {
			directories++
		}
		if kind == "file" {
			if !logical.Valid {
				unknown++
			} else {
				setDecimal(&measured, logical.String)
				sum.Add(sum, &measured)
			}
		}
		count++
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if count != h.Entries || sum.String() != string(h.LogicalBytes) {
		return ErrGeneration
	}
	// Every node already has an existing lower-ID directory parent in the same root.
	// Therefore only directories can extend ancestry: validating their bounded reachability
	// also proves all terminal children reachable, including leaves at depth 257. Avoid
	// materializing every file again in SQLite's recursive temporary work queue.
	var reachable uint64
	if err = tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id,depth) AS (SELECT id,0 FROM nodes WHERE parent_id IS NULL AND kind='directory' UNION ALL SELECT n.id,t.depth+1 FROM nodes n JOIN tree t ON n.parent_id=t.id WHERE n.kind='directory' AND t.depth<256) SELECT count(*) FROM tree`).Scan(&reachable); err != nil {
		return err
	}
	if reachable != directories {
		return ErrGeneration
	}
	if err = validateProvenanceRows(ctx, tx, h); err != nil {
		return err
	}
	if err = validateAggregates(ctx, tx, h.FileTypeMapping); err != nil {
		return err
	}
	h.UnknownLogicalEntries = unknown
	rows, err = tx.QueryContext(ctx, `SELECT target_id,state,errors FROM coverage ORDER BY target_id`)
	if err != nil {
		return err
	}
	h.Coverage = nil
	var coverageErrors uint64
	for rows.Next() {
		var c CoverageRecord
		if err = rows.Scan(&c.TargetID, &c.State, &c.Errors); err != nil {
			break
		}
		if !expected[c.TargetID] || (c.State == "complete") != (c.Errors == 0) {
			err = ErrGeneration
			break
		}
		if c.State == "complete" && !rootSeen[c.TargetID] {
			err = ErrGeneration
			break
		}
		if endTargets[c.TargetID] && (c.State != "partial" || c.Errors == 0) {
			err = ErrGeneration
			break
		}
		delete(expected, c.TargetID)
		coverageErrors += c.Errors
		h.Coverage = append(h.Coverage, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	// Manifest issues can add errors beyond per-root traversal counters.
	if len(expected) != 0 || coverageErrors > h.CoverageErrors || (len(h.Manifest.Issues) > 0 && h.State == "complete") {
		return ErrGeneration
	}
	// Validate every boundary using a streaming cursor before sealing its exact count.
	// Missing covered targets remain manifest-qualified even when they have no traversal root.
	boundaryTargets := make(map[string]scopemanifest.ManifestRoot, len(roots)+len(h.Manifest.Issues))
	for id, root := range roots {
		boundaryTargets[id] = root
	}
	coveredTargets := map[string]bool{}
	for _, id := range h.Manifest.CoveredTargetIDs {
		coveredTargets[id] = true
	}
	for _, issue := range h.Manifest.Issues {
		if _, root := roots[issue.TargetID]; !root && !coveredTargets[issue.TargetID] {
			return ErrGeneration
		}
		boundaryTargets[issue.TargetID] = scopemanifest.ManifestRoot{}
	}
	boundaryCount, err := validateBoundaries(ctx, tx, boundaryTargets)
	if err != nil {
		return err
	}
	h.ScanBoundaryCount = contracts.Bytes(boundaryCount)
	if !seal {
		if !reflect.DeepEqual(original, h) {
			return ErrGeneration
		}
		return ctx.Err()
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO generation_header VALUES(1,?)`, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// ReadGenerationHeader requires the current sealed schema without changing older files.
func ReadGenerationHeader(ctx context.Context, db *sql.DB, snapshot string) (GenerationHeader, error) {
	var h GenerationHeader
	var version int
	var raw string
	if db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version) != nil || version != GenerationSchema || db.QueryRowContext(ctx, `SELECT header_json FROM generation_header WHERE singleton=1`).Scan(&raw) != nil || json.Unmarshal([]byte(raw), &h) != nil || h.SchemaVersion != GenerationSchema || h.AggregateVersion != AggregateVersion || h.SnapshotID != snapshot || h.ScanBoundaryCount.Validate() != nil || ValidateSourceProvenance(h.Manifest) != nil {
		return h, ErrGeneration
	}
	return h, nil
}

// validGenerationMethods preserves old logical-only headers without inventing historical qualification.
func validGenerationMethods(methods []string) bool {
	return len(methods) == 1 && methods[0] == "descriptor-no-follow-stat" || len(methods) == 2 && methods[0] == "descriptor-no-follow-stat" && methods[1] == contracts.AllocatedQualification
}
