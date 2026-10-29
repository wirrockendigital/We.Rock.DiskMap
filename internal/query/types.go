// Package query serves bounded immutable index projections without source filesystem access.
package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

const MaxElements = 10000
const MaxResponseBytes = 4 << 20
const MaxDepth = 8
const QueryTimeout = 3 * time.Second

// ExpansionBudget leaves time for exact residual groups, encoding and the authenticated response.
const ExpansionBudget = 600 * time.Millisecond

var ErrRequest = errors.New("INVALID_HIERARCHY_REQUEST")
var ErrSnapshot = errors.New("SNAPSHOT_MISMATCH")
var ErrRoot = errors.New("HIERARCHY_ROOT_NOT_FOUND")
var ErrBudget = errors.New("QUERY_BUDGET_EXCEEDED")

// Filter retains ancestor directories; category selection applies to regular-file payloads only.
type Filter struct {
	Categories   []string         `json:"categories"`
	MinSize      *contracts.Bytes `json:"minSize,omitempty"`
	MaxSize      *contracts.Bytes `json:"maxSize,omitempty"`
	ModifiedFrom *string          `json:"modifiedFrom,omitempty"`
	ModifiedTo   *string          `json:"modifiedTo,omitempty"`
	ObjectKinds  []string         `json:"objectKinds,omitempty"`
	Text         string           `json:"text,omitempty"`
	Field        string           `json:"field,omitempty"`
}
type Request struct {
	Mode          string `json:"mode,omitempty"`
	SnapshotID    string `json:"snapshotId"`
	RootID        string `json:"rootId"`
	Metric        string `json:"metric"`
	Depth         int    `json:"depth"`
	ElementBudget int    `json:"elementBudget"`
	Filter        Filter `json:"filter"`
}

// Summary counts path entries, including a real directory itself, without double-counting bytes.
type Summary struct {
	KnownBytes       contracts.Bytes `json:"knownBytes"`
	UnknownFileCount contracts.Bytes `json:"unknownFileCount"`
	FileCount        contracts.Bytes `json:"fileCount"`
	DirectoryCount   contracts.Bytes `json:"directoryCount"`
	SpecialCount     contracts.Bytes `json:"specialCount"`
}

// Node is a display projection. Virtual containers have no real node ID or filesystem path.
type Node struct {
	// ColorBranchKey is an opaque historical color identity, never a navigation ID.
	ColorBranchKey *string           `json:"colorBranchKey"`
	Provenance     *SourceProvenance `json:"provenance,omitempty"`
	Classification *Classification   `json:"classification"`
	ID             string            `json:"id"`
	ParentID       *string           `json:"parentId"`
	RealNodeID     *string           `json:"realNodeId"`
	Name           string            `json:"name"`
	// ModifiedAt is explicitly unknown for the minimal metadata schema.
	ModifiedAt      *string         `json:"modifiedAt"`
	Kind            string          `json:"kind"`
	VolumeID        string          `json:"volumeId,omitempty"`
	ShareID         *string         `json:"shareId,omitempty"`
	Virtual         bool            `json:"virtual"`
	Summary         Summary         `json:"summary"`
	OriginalSummary Summary         `json:"originalSummary"`
	MatchSummary    Summary         `json:"matchSummary"`
	MatchKind       string          `json:"matchKind"`
	HasMore         bool            `json:"hasMore"`
	GroupReason     string          `json:"groupReason,omitempty"`
	GroupedChildren contracts.Bytes `json:"groupedChildren,omitempty"`
	ListRootID      string          `json:"listRootId,omitempty"`
	AfterCursor     string          `json:"afterCursor,omitempty"`
}

// Response repeats the effective immutable query identity and does not conflate reduced display with coverage.
type Response struct {
	SourceAvailability SourceAvailability     `json:"sourceAvailability"`
	Mode               string                 `json:"mode"`
	OriginalSummary    Summary                `json:"originalSummary"`
	MatchSummary       Summary                `json:"matchSummary"`
	Denominator        Summary                `json:"denominator"`
	MatchState         string                 `json:"matchState"`
	SnapshotID         string                 `json:"snapshotId"`
	RootID             string                 `json:"rootId"`
	Metric             string                 `json:"metric"`
	Filter             Filter                 `json:"filter"`
	Depth              int                    `json:"depth"`
	ElementBudget      int                    `json:"elementBudget"`
	SelectionOrder     string                 `json:"selectionOrder"`
	Nodes              []Node                 `json:"nodes"`
	Truncated          bool                   `json:"truncated"`
	State              string                 `json:"state"`
	Coverage           []index.CoverageRecord `json:"coverage"`
}

// Page lists recorded direct children. Its summary is not an invented total for an incomplete page.
type Page struct {
	Mode            string  `json:"mode"`
	OriginalSummary Summary `json:"originalSummary"`
	MatchSummary    Summary `json:"matchSummary"`
	Denominator     Summary `json:"denominator"`
	MatchState      string  `json:"matchState"`
	SnapshotID      string  `json:"snapshotId"`
	RootID          string  `json:"rootId"`
	Metric          string  `json:"metric"`
	Filter          Filter  `json:"filter"`
	Nodes           []Node  `json:"nodes"`
	HasMore         bool    `json:"hasMore"`
	NextCursor      string  `json:"nextCursor"`
	SelectionOrder  string  `json:"selectionOrder"`
}

var categories = map[string]bool{"image": true, "video": true, "audio": true, "document": true, "archive": true, "diskImage": true, "database": true, "otherUnknown": true}

// Validate copies and canonicalizes the filter so callers cannot mutate in-flight query identity.
func (r Request) Validate() (Request, error) {
	if r.SnapshotID == "" || len(r.SnapshotID) > 128 || r.RootID == "" || len(r.RootID) > 160 || (r.Metric != "logical" && r.Metric != "allocated") || r.Depth < 1 || r.Depth > MaxDepth || r.ElementBudget < 1 || r.ElementBudget > MaxElements || len(r.Filter.Categories) > 8 {
		return r, ErrRequest
	}
	var err error
	r.Filter, err = r.Filter.Canonical()
	if err != nil {
		return r, err
	}
	if r.Mode == "" {
		r.Mode = "highlight"
	}
	if r.Mode != "highlight" && r.Mode != "hits-only" {
		return r, ErrRequest
	}
	return r, nil
}

// checkHeader verifies the caller's sealed generation identity before any child lookup.
func checkHeader(ctx context.Context, db *sql.DB, h index.GenerationHeader, r Request) error {
	if h.SnapshotID != r.SnapshotID || h.SchemaVersion != index.GenerationSchema || (h.State != "complete" && h.State != "partial") {
		return ErrSnapshot
	}
	var id string
	if err := db.QueryRowContext(ctx, `SELECT snapshot_id FROM generation_identity WHERE singleton=1`).Scan(&id); err != nil {
		return err
	}
	if id != r.SnapshotID {
		return ErrSnapshot
	}
	stored, err := index.ReadGenerationHeader(ctx, db, r.SnapshotID)
	if err != nil || !reflect.DeepEqual(stored, h) {
		return ErrSnapshot
	}
	return nil
}

// cursor identity includes every semantic selector but permits changing the page size or display depth.
func cursorKey(r Request) string {
	b, _ := json.Marshal(struct {
		Projection                   int
		Snapshot, Root, Metric, Mode string
		Filter                       Filter
	}{1, r.SnapshotID, r.RootID, r.Metric, r.Mode, r.Filter})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func cursor(r Request, row int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorKey(r) + ":" + strconv.FormatInt(row, 10)))
}
func parseCursor(r Request, c string) (int64, error) {
	if c == "" {
		return 0, nil
	}
	if len(c) > 160 {
		return 0, ErrRequest
	}
	b, e := base64.RawURLEncoding.DecodeString(c)
	if e != nil || len(b) < 66 || string(b[:65]) != cursorKey(r)+":" {
		return 0, ErrRequest
	}
	n, e := strconv.ParseInt(string(b[65:]), 10, 64)
	if e != nil || strconv.FormatInt(n, 10) != string(b[65:]) {
		return 0, ErrRequest
	}
	return n, nil
}
