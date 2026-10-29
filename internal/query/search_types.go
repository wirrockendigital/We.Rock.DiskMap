package query

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const MaxSearchResults = 50
const MaxSearchCursorBytes = 512

var ErrSearch = errors.New("INVALID_SEARCH_REQUEST")

// SearchRequest selects one immutable view and an explicit direct-child or recursive result scope.
type SearchRequest struct {
	// Optional list selectors are cursor-bound and do not change stored preferences.
	MatchMode    string `json:"matchMode,omitempty"`
	FoldersFirst bool   `json:"foldersFirst,omitempty"`
	CategorySort bool   `json:"categorySort,omitempty"`
	Filter       Filter `json:"filter"`
	SnapshotID   string `json:"snapshotId"`
	RootID       string `json:"rootId"`
	Scope        string `json:"scope"`
	Text         string `json:"text"`
	Field        string `json:"field"`
	Metric       string `json:"metric"`
	Sort         string `json:"sort"`
	Direction    string `json:"direction"`
	Limit        int    `json:"limit"`
}

// SearchItem returns public index metadata and exact selected-metric subtree totals.
// Size is null for unmeasured/special objects or a directory with unknown payloads.
type SearchItem struct {
	Classification *Classification `json:"classification"`
	rawName        []byte
	SnapshotID     string           `json:"snapshotId"`
	NodeID         string           `json:"nodeId"`
	ParentID       *string          `json:"parentId"`
	Name           string           `json:"name"`
	Path           string           `json:"path"`
	Kind           string           `json:"kind"`
	VolumeID       string           `json:"volumeId"`
	ShareID        *string          `json:"shareId"`
	ModifiedAt     *string          `json:"modifiedAt"`
	Size           *contracts.Bytes `json:"size"`
	Summary        Summary          `json:"summary"`
}

// SearchPage never calls a budget-limited empty intermediate page a complete no-match result.
type SearchPage struct {
	SnapshotID     string       `json:"snapshotId"`
	RootID         string       `json:"rootId"`
	Items          []SearchItem `json:"items"`
	HasMore        bool         `json:"hasMore"`
	NextCursor     string       `json:"nextCursor"`
	WorkLimited    bool         `json:"workLimited"`
	SelectionOrder string       `json:"selectionOrder"`
	Metric         string       `json:"metric"`
	Scope          string       `json:"scope"`
}

// Validate bounds work and treats all search text as literal Unicode, including SQL and wildcard characters.
func (r SearchRequest) Validate() (SearchRequest, error) {
	if r.SnapshotID == "" || len(r.SnapshotID) > 128 || r.RootID == "" || len(r.RootID) > 160 || len(r.Text) > 512 || !utf8.ValidString(r.Text) || r.Limit < 1 || r.Limit > MaxSearchResults {
		return r, ErrSearch
	}
	if r.Scope != "direct" && r.Scope != "recursive" {
		return r, ErrSearch
	}
	if r.Field != "name" && r.Field != "path" && r.Field != "both" {
		return r, ErrSearch
	}
	if r.Metric != "logical" && r.Metric != "allocated" {
		return r, ErrSearch
	}
	if r.Direction != "asc" && r.Direction != "desc" {
		return r, ErrSearch
	}
	switch r.Sort {
	case "name", "size", "type":
	default:
		return r, ErrSearch
	}
	if r.MatchMode != "" && r.MatchMode != "literal" && r.MatchMode != "glob" {
		return r, ErrSearch
	}
	var err error
	r.Filter, err = r.Filter.Canonical()
	if err != nil {
		return r, ErrSearch
	}
	return r, nil
}

// searchSelector binds continuation to every semantic selector; changing page size is allowed.
func searchSelector(r SearchRequest) string {
	r.Filter, _ = r.Filter.Canonical()
	r.Limit = 0
	raw, _ := json.Marshal(r)
	return fmt.Sprintf("%x", sha256.Sum256(append([]byte("source-projection:1:"), raw...)))
}

type searchPosition struct {
	Selector string `json:"selector"`
	Key      []byte `json:"-"`
	Row      string `json:"row"`
}

// encodeSearchCursor distinguishes the first page from every signed SQLite rowid, including zero.
func encodeSearchCursor(r SearchRequest, row int64) string {
	raw, _ := json.Marshal(searchPosition{Selector: searchSelector(r), Row: strconv.FormatInt(row, 10)})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeSearchCursor(r SearchRequest, value string) (searchPosition, int64, error) {
	if value == "" {
		return searchPosition{}, 0, nil
	}
	if len(value) > MaxSearchCursorBytes {
		return searchPosition{}, 0, ErrSearch
	}
	raw, e := base64.RawURLEncoding.DecodeString(value)
	if e != nil {
		return searchPosition{}, 0, ErrSearch
	}
	var p searchPosition
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Selector != searchSelector(r) {
		return p, 0, ErrSearch
	}
	row, e := strconv.ParseInt(p.Row, 10, 64)
	if e != nil || row < 1 || strconv.FormatInt(row, 10) != p.Row {
		return p, 0, ErrSearch
	}
	return p, row, nil
}
