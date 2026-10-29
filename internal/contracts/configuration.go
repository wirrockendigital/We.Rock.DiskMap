package contracts

// Configuration limits apply independently to the downloadable bundle and transport envelope.
const (
	ConfigurationBundleLimit   = 4 * 1024 * 1024
	ConfigurationEnvelopeLimit = ConfigurationBundleLimit + 4096
	ConfigurationProfileLimit  = 200
)

// ConfigurationProfileSettings contains portable intent, never physical source bindings.
type ConfigurationProfileSettings struct {
	Name             string         `json:"name"`
	Scope            Scope          `json:"scope"`
	Active           bool           `json:"active"`
	Exclusions       []Exclusion    `json:"exclusions"`
	ResourceMode     string         `json:"resourceMode"`
	Limits           ResourceLimits `json:"limits"`
	FreshnessSeconds int            `json:"freshnessSeconds"`
}

// ConfigurationProfile retains only a source reference for explicit import mapping.
type ConfigurationProfile struct {
	SourceProfileID string                       `json:"sourceProfileId"`
	Configuration   ConfigurationProfileSettings `json:"configuration"`
}

// ConfigurationTemplate excludes the database's mutable CAS revision.
type ConfigurationTemplate struct {
	Rules []Exclusion `json:"rules"`
}

// ConfigurationGlobal contains eligible shared configuration rather than runtime status.
type ConfigurationGlobal struct {
	ExclusionTemplate      ConfigurationTemplate `json:"exclusionTemplate"`
	RetentionCompleteCount int                   `json:"retentionCompleteCount"`
	FileTypeMapping        FileTypeMapping       `json:"fileTypeMapping"`
}

// ConfigurationPersonal deliberately omits principal, revision, remembered view and leases.
type ConfigurationPersonal struct {
	Appearance          PreferenceAppearance `json:"appearance"`
	Display             PreferenceDisplay    `json:"display"`
	PreferredProfileRef *string              `json:"preferredProfileRef"`
}

// ConfigurationBundle is the standalone versioned download, not an API envelope.
type ConfigurationBundle struct {
	FormatVersion int                    `json:"formatVersion"`
	ExportedAt    string                 `json:"exportedAt"`
	Profiles      []ConfigurationProfile `json:"profiles"`
	Global        ConfigurationGlobal    `json:"global"`
	Personal      ConfigurationPersonal  `json:"personal"`
}

// ConfigurationExport provides one whole bundle; no partial-export representation exists.
type ConfigurationExport struct {
	Bundle ConfigurationBundle `json:"bundle"`
}
