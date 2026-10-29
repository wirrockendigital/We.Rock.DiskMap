package contracts

// PreferenceWorkspace stores presentation state under the authenticated DSM principal.
// Remembered tree identifiers are selectors, never filesystem access authority.
type PreferenceWorkspace struct {
	// Empty selects mixed until the principal saves an explicit display unit.
	SizeUnit string `json:"sizeUnit,omitempty"`
	// Nil preserves visible share status for workspaces saved before this field existed.
	ShowShareStatus *bool `json:"showShareStatus,omitempty"`
	// Remembered source selectors are requalified against the live inventory on reopen.
	Source *Scope `json:"source,omitempty"`
	// Nil selects the explicit default: include categories in newly created scan profiles.
	ScanRecycle *bool `json:"scanRecycle,omitempty"`
	ScanSystem  *bool `json:"scanSystem,omitempty"`
	// Pending job identities let completion observations survive a closed DSM window.
	Jobs          []string       `json:"jobs"`
	Zoom          *WorkspaceZoom `json:"zoom"`
	Left          bool           `json:"left"`
	Right         bool           `json:"right"`
	LeftWidth     int            `json:"leftWidth"`
	RightWidth    int            `json:"rightWidth"`
	Expanded      []string       `json:"expanded"`
	TreeSort      string         `json:"treeSort"`
	TreeDirection string         `json:"treeDirection"`
	TreeScroll    int            `json:"treeScroll"`
	ListScroll    int            `json:"listScroll"`
	FilterOpen    bool           `json:"filterOpen"`
	List          WorkspaceList  `json:"list"`
	ShowRecycle   bool           `json:"showRecycle"`
	ShowSystem    bool           `json:"showSystem"`
}

// WorkspaceList mirrors the supported indexed list query selectors.
type WorkspaceList struct {
	Text         string `json:"text"`
	Recursive    bool   `json:"recursive"`
	Kind         string `json:"kind"`
	Category     string `json:"category"`
	Sort         string `json:"sort"`
	Direction    string `json:"direction"`
	FoldersFirst bool   `json:"foldersFirst"`
}

// WorkspaceZoom is bounded presentation geometry for one qualified snapshot root.
type WorkspaceZoom struct {
	SnapshotID string  `json:"snapshotId,omitempty"`
	Root       string  `json:"root"`
	Scale      float64 `json:"scale"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
}
