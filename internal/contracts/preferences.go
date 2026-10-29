package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
)

// PreferenceFilter mirrors the existing query filter without introducing a package cycle.
// Query's canonical validator remains the authority for its semantic constraints.
type PreferenceFilter struct {
	Categories   []string `json:"categories"`
	MinSize      *Bytes   `json:"minSize,omitempty"`
	MaxSize      *Bytes   `json:"maxSize,omitempty"`
	ModifiedFrom *string  `json:"modifiedFrom,omitempty"`
	ModifiedTo   *string  `json:"modifiedTo,omitempty"`
	ObjectKinds  []string `json:"objectKinds,omitempty"`
	Text         string   `json:"text,omitempty"`
	Field        string   `json:"field,omitempty"`
}

// PreferenceSearch stores selectors, never a result page or continuation cursor.
type PreferenceSearch struct {
	Text  string `json:"text"`
	Field string `json:"field"`
	Scope string `json:"scope"`
}

// PreferenceView is a remembered intent; it does not attest that a generation still exists.
type PreferenceView struct {
	ProfileID      string           `json:"profileId"`
	SnapshotID     string           `json:"snapshotId"`
	RootID         string           `json:"rootId"`
	SelectedNodeID *string          `json:"selectedNodeId"`
	Map            string           `json:"map"`
	Metric         string           `json:"metric"`
	FilterMode     string           `json:"filterMode"`
	Filter         PreferenceFilter `json:"filter"`
	Search         PreferenceSearch `json:"search"`
	Sort           string           `json:"sort"`
	Direction      string           `json:"direction"`
}

// PreferenceAppearance is updated as one CAS-protected field group.
type PreferenceAppearance struct {
	Language string  `json:"language"`
	Theme    string  `json:"theme"`
	Map      string  `json:"map"`
	Locale   *string `json:"locale"`
	TimeZone *string `json:"timeZone"`
}

// PreferenceVersion is independent of the outer API envelope version.
const PreferenceVersion = 2

// PreferenceDetail preserves the wire distinction between modes and numeric depths.
type PreferenceDetail string

// MarshalJSON emits explicit depth values as numbers, not numeric strings.
func (d PreferenceDetail) MarshalJSON() ([]byte, error) {
	if len(d) == 1 && d[0] >= '1' && d[0] <= '8' {
		return []byte(d), nil
	}
	return json.Marshal(string(d))
}

// UnmarshalJSON admits only the documented finite modes and integer depths.
func (d *PreferenceDetail) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte(`"auto"`)) {
		*d = "auto"
		return nil
	}
	if bytes.Equal(raw, []byte(`"max"`)) {
		*d = "max"
		return nil
	}
	if len(raw) == 1 && raw[0] >= '1' && raw[0] <= '8' {
		*d = PreferenceDetail(raw)
		return nil
	}
	return errors.New("INVALID_PREFERENCE_DETAIL")
}

// PreferenceDisplay is one complete CAS-protected group; borders cannot be disabled.
type PreferenceDisplay struct {
	Units            string           `json:"units"`
	ColorMode        string           `json:"colorMode"`
	Detail           PreferenceDetail `json:"detail"`
	Labels           bool             `json:"labels"`
	Percentages      bool             `json:"percentages"`
	HierarchyBorders string           `json:"hierarchyBorders"`
	ReducedMotion    string           `json:"reducedMotion"`
}

// UnmarshalJSON rejects missing/null booleans instead of treating omission as false.
func (d *PreferenceDisplay) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Units            string           `json:"units"`
		ColorMode        string           `json:"colorMode"`
		Detail           PreferenceDetail `json:"detail"`
		Labels           *bool            `json:"labels"`
		Percentages      *bool            `json:"percentages"`
		HierarchyBorders string           `json:"hierarchyBorders"`
		ReducedMotion    string           `json:"reducedMotion"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return err
	}
	if wire.Labels == nil || wire.Percentages == nil {
		return errors.New("INVALID_PREFERENCE_DISPLAY")
	}
	*d = PreferenceDisplay{wire.Units, wire.ColorMode, wire.Detail, *wire.Labels, *wire.Percentages, wire.HierarchyBorders, wire.ReducedMotion}
	return nil
}

// DefaultPreferenceDisplay returns a value, so callers cannot mutate shared defaults.
func DefaultPreferenceDisplay() PreferenceDisplay {
	return PreferenceDisplay{Units: "decimal", ColorMode: "folder", Detail: "auto", Labels: true, Percentages: true, HierarchyBorders: "normal", ReducedMotion: "auto"}
}
