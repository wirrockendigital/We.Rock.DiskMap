package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxPreferenceRevision = 2147483647

var ErrPreferenceConflict = errors.New("PREFERENCE_CONFLICT")

// Nullable field groups distinguish omission from an explicit clearing operation.
type PreferredProfileChange struct {
	ProfileID *string `json:"profileId"`
}
type PreferenceViewChange struct {
	Value *contracts.PreferenceView `json:"value"`
}
type PreferenceRequest struct {
	Workspace        *contracts.PreferenceWorkspace  `json:"workspace,omitempty"`
	Display          *contracts.PreferenceDisplay    `json:"display,omitempty"`
	ContractVersion  int                             `json:"contractVersion"`
	ExpectedRevision int                             `json:"expectedRevision"`
	Group            string                          `json:"group"`
	Appearance       *contracts.PreferenceAppearance `json:"appearance,omitempty"`
	PreferredProfile *PreferredProfileChange         `json:"preferredProfile,omitempty"`
	View             *PreferenceViewChange           `json:"view,omitempty"`
}

// Validate shares the production query selectors and rejects foreign owner fields at strict decoding.
func (r PreferenceRequest) Validate() error {
	if r.ContractVersion != contracts.PreferenceVersion || r.ExpectedRevision < 0 || r.ExpectedRevision >= MaxPreferenceRevision {
		return ErrInvalid
	}
	if r.Group != "workspace" && r.Workspace != nil {
		return ErrInvalid
	}
	switch r.Group {
	// New display choices cannot request metadata omitted by the compact index.
	case "workspace":
		if r.Workspace == nil || r.Display != nil || r.Appearance != nil || r.PreferredProfile != nil || r.View != nil || !validWorkspace(*r.Workspace) {
			return ErrInvalid
		}
	case "display":
		if r.Display == nil || r.Appearance != nil || r.PreferredProfile != nil || r.View != nil || !validPreferenceDisplay(*r.Display) || r.Display.ColorMode == "mtime" {
			return ErrInvalid
		}
	case "appearance":
		if r.Display != nil || r.Appearance == nil || r.PreferredProfile != nil || r.View != nil {
			return ErrInvalid
		}
		a := r.Appearance
		if !oneOf(a.Language, "auto", "de", "en") || !oneOf(a.Theme, "auto", "light", "dark") || !oneOf(a.Map, "treemap", "radial") {
			return ErrInvalid
		}
		if a.Locale != nil && !oneOf(*a.Locale, "de-DE", "en-US", "en-GB") {
			return ErrInvalid
		}
		if a.TimeZone != nil {
			if len(*a.TimeZone) > 128 || *a.TimeZone == "" || strings.ContainsAny(*a.TimeZone, "\\\x00") {
				return ErrInvalid
			}
			if _, e := time.LoadLocation(*a.TimeZone); e != nil {
				return ErrInvalid
			}
		}
	case "preferredProfile":
		if r.Display != nil || r.PreferredProfile == nil || r.Appearance != nil || r.View != nil || r.PreferredProfile.ProfileID != nil && !security.ValidID(*r.PreferredProfile.ProfileID) {
			return ErrInvalid
		}
	case "lastView":
		if r.Display != nil || r.View == nil || r.Appearance != nil || r.PreferredProfile != nil {
			return ErrInvalid
		}
		if r.View.Value != nil {
			return validatePreferenceView(*r.View.Value)
		}
	default:
		return ErrInvalid
	}
	return nil
}

// oneOf checks exact enums without accepting case aliases.
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// Roots may be synthetic hierarchy identities; none may be paths or unbounded display text.
func preferenceNodeID(s string) bool {
	if len(s) == 0 || len(s) > 160 || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-:.", c)) {
			return false
		}
	}
	return true
}

// The existing query validator owns filter, metric, search, and sort semantics.
func validatePreferenceView(v contracts.PreferenceView) error {
	if !security.ValidID(v.ProfileID) || !security.ValidID(v.SnapshotID) || !preferenceNodeID(v.RootID) || v.SelectedNodeID != nil && !preferenceNodeID(*v.SelectedNodeID) || !oneOf(v.Map, "treemap", "radial") || !oneOf(v.FilterMode, "highlight", "hits-only") {
		return ErrInvalid
	}
	_, e := (query.SearchRequest{SnapshotID: v.SnapshotID, RootID: v.RootID, Metric: v.Metric, Filter: query.Filter(v.Filter), Text: v.Search.Text, Field: v.Search.Field, Scope: v.Search.Scope, Sort: v.Sort, Direction: v.Direction, Limit: 50}).Validate()
	if e != nil {
		return ErrInvalid
	}
	return nil
}

// Defaults are calculated without inserting rows or manufacturing a profile or snapshot.
func defaultPreferences(principal string) contracts.UserPreference {
	return contracts.UserPreference{Version: contracts.PreferenceVersion, Display: contracts.DefaultPreferenceDisplay(), DSMUserID: principal, Language: "auto", Theme: "auto", Map: "treemap"}
}

type preferenceReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// readPreferences distinguishes an absent row from a malformed persisted value.
func readPreferences(ctx context.Context, db preferenceReader, principal string) (contracts.UserPreference, error) {
	out := defaultPreferences(principal)
	var raw string
	var revision int
	e := db.QueryRowContext(ctx, "SELECT revision,value_json FROM user_preferences WHERE principal=?", principal).Scan(&revision, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	// Existing rows must provide their own required fields; only absent rows receive defaults.
	out = contracts.UserPreference{}
	if len(raw) > 16384 || security.DecodeJSON(strings.NewReader(raw), 16384, &out) != nil || out.Version != contracts.PreferenceVersion || out.DSMUserID != principal || revision < 1 || revision > MaxPreferenceRevision || out.Revision != revision {
		return contracts.UserPreference{}, ErrUnavailable
	}
	if !ValidPreference(out, principal) {
		return contracts.UserPreference{}, ErrUnavailable
	}
	return out, nil
}

// Preferences uses the authenticated principal only; a missing record has no write side effect.
func (m *Manager) Preferences(ctx context.Context, principal string) (contracts.UserPreference, error) {
	if !validPrincipal(principal) {
		return contracts.UserPreference{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return contracts.UserPreference{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return readPreferences(ctx, m.db, principal)
}

// SetPreferences commits one field group and immutable replay evidence in the same transaction.
func (m *Manager) SetPreferences(ctx context.Context, in PreferenceRequest, principal, key string) (contracts.UserPreference, error) {
	var zero contracts.UserPreference
	if in.Validate() != nil || !validPrincipal(principal) {
		return zero, ErrInvalid
	}
	raw, e := json.Marshal(in)
	if e != nil || len(raw) > 16384 {
		return zero, ErrInvalid
	}
	replay := ReplayRequest{principal, "preferences.update", key, string(raw)}
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
		if security.DecodeJSON(strings.NewReader(string(previous)), 16384, &zero) != nil || !ValidPreferenceReceipt(zero, principal, in) {
			return zero, ErrUnavailable
		}
		return zero, nil
	}
	out, e := readPreferences(ctx, tx, principal)
	if e != nil {
		return zero, e
	}
	if out.Revision != in.ExpectedRevision {
		return zero, ErrPreferenceConflict
	}
	var profile *string
	switch in.Group {
	case "workspace":
		out.Workspace = in.Workspace
	case "display":
		out.Display = *in.Display
	case "appearance":
		a := in.Appearance
		out.Language = a.Language
		out.Theme = a.Theme
		out.Map = a.Map
		out.Locale = a.Locale
		out.TimeZone = a.TimeZone
	case "preferredProfile":
		out.PreferredProfileID = in.PreferredProfile.ProfileID
		profile = out.PreferredProfileID
	case "lastView":
		out.LastView = in.View.Value
		if out.LastView != nil {
			profile = &out.LastView.ProfileID
		}
	}
	// Only the changed group is reference-checked; stale remembered values remain readable for WI061 recovery.
	if profile != nil {
		var exists int
		if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM profiles WHERE id=?", *profile).Scan(&exists); e != nil {
			return zero, e
		}
		if exists != 1 {
			return zero, ErrInvalid
		}
	}
	out.Revision++
	encoded, e := json.Marshal(out)
	if e != nil || len(encoded) > 16384 {
		return zero, ErrInvalid
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO user_preferences(principal,revision,value_json) VALUES(?,?,?) ON CONFLICT(principal) DO UPDATE SET revision=excluded.revision,value_json=excluded.value_json`, principal, out.Revision, string(encoded)); e != nil {
		return zero, e
	}
	if e = RecordReplay(tx, replay, encoded); e != nil {
		return zero, e
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return out, nil
}

// ValidPreference verifies bounded stored and transported values, including their authenticated owner.
func ValidPreference(out contracts.UserPreference, principal string) bool {
	if out.Workspace != nil && !validWorkspace(*out.Workspace) {
		return false
	}
	if !validPreferenceDisplay(out.Display) || !validPrincipal(principal) || out.DSMUserID != principal || out.Version != contracts.PreferenceVersion || out.Revision < 0 || out.Revision > MaxPreferenceRevision {
		return false
	}
	appearance := contracts.PreferenceAppearance{Language: out.Language, Theme: out.Theme, Map: out.Map, Locale: out.Locale, TimeZone: out.TimeZone}
	if (PreferenceRequest{ContractVersion: contracts.PreferenceVersion, Group: "appearance", Appearance: &appearance}).Validate() != nil || out.PreferredProfileID != nil && !security.ValidID(*out.PreferredProfileID) || out.LastView != nil && validatePreferenceView(*out.LastView) != nil {
		return false
	}
	raw, e := json.Marshal(out)
	return e == nil && len(raw) <= 16384
}

// validPreferenceDisplay shares exact enums between persistence and public receipts.
func validPreferenceDisplay(d contracts.PreferenceDisplay) bool {
	return oneOf(d.Units, "decimal", "binary") && oneOf(d.ColorMode, "folder", "filetype", "mtime") && oneOf(string(d.Detail), "auto", "max", "1", "2", "3", "4", "5", "6", "7", "8") && oneOf(d.HierarchyBorders, "normal", "subtle") && oneOf(d.ReducedMotion, "auto", "reduce")
}

// ValidPreferenceReceipt binds a successful mutation reply to its principal, revision and field group.
func ValidPreferenceReceipt(out contracts.UserPreference, principal string, in PreferenceRequest) bool {
	return in.Validate() == nil && ValidPreference(out, principal) && out.Revision == in.ExpectedRevision+1 && preferenceMatchesGroup(out, in)
}

// validWorkspace bounds user-controlled presentation data before it reaches persistent storage.
func validWorkspace(w contracts.PreferenceWorkspace) bool {
	if len(w.Jobs) > 32 {
		return false
	}
	for _, job := range w.Jobs {
		parts := strings.Split(job, "|")
		if len(parts) != 2 || !security.ValidID(parts[0]) || !security.ValidID(parts[1]) {
			return false
		}
	}
	if s := w.Source; s != nil {
		if !oneOf(s.Kind, "nas", "volumes", "shares", "folder") || s.Kind == "nas" && len(s.TargetIDs) != 0 || s.Kind != "nas" && len(s.TargetIDs) != 1 {
			return false
		}
		for _, id := range s.TargetIDs {
			if !security.ValidID(id) {
				return false
			}
		}
		if s.Kind == "folder" && !contracts.ValidFolderScope(*s) {
			return false
		}
	}
	if z := w.Zoom; z != nil {
		if z.SnapshotID != "" && !security.ValidID(z.SnapshotID) {
			return false
		}
		if !security.ValidID(z.Root) || z.Scale < 1 || z.Scale > 64 || z.X > 0 || z.Y > 0 || z.X < -1000000 || z.Y < -1000000 {
			return false
		}
	}
	if !oneOf(w.SizeUnit, "", "TB", "GB", "MB", "mixed", "hidden") {
		return false
	}
	if w.LeftWidth < 180 || w.LeftWidth > 700 || w.RightWidth < 240 || w.RightWidth > 800 || len(w.Expanded) > 256 || !oneOf(w.TreeSort, "name", "size") || !oneOf(w.TreeDirection, "asc", "desc") || w.TreeScroll < 0 || w.TreeScroll > 100000000 || w.ListScroll < 0 || w.ListScroll > 100000000 {
		return false
	}
	seen := map[string]bool{}
	for _, s := range w.Expanded {
		if s == "" || len(s) > 4096 || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") || seen[s] {
			return false
		}
		seen[s] = true
	}
	l := w.List
	return utf8.ValidString(l.Text) && len(l.Text) <= 512 && !strings.ContainsRune(l.Text, 0) && oneOf(l.Kind, "all", "file", "directory") && oneOf(l.Category, "", "image", "video", "audio", "document", "archive", "diskImage", "database", "otherUnknown") && oneOf(l.Sort, "name", "size", "type") && oneOf(l.Direction, "asc", "desc")
}
