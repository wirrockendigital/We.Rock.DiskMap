package contracts

// Import transport adds a bounded mapping/control allowance to the independent bundle limit.
const (
	ConfigurationImportLimit  = ConfigurationBundleLimit + 512*1024
	ConfigurationPreviewLimit = 1024 * 1024
	ConfigurationMappingLimit = 1536
)

// ConfigurationSettingsSelection requires explicit opt-in for every independent settings group.
type ConfigurationSettingsSelection struct {
	Appearance        bool `json:"appearance"`
	Display           bool `json:"display"`
	PreferredProfile  bool `json:"preferredProfile"`
	ExclusionTemplate bool `json:"exclusionTemplate"`
	Retention         bool `json:"retention"`
	FileTypeMapping   bool `json:"fileTypeMapping"`
}

// ConfigurationSelection retains source IDs only as references, never destination identities.
type ConfigurationSelection struct {
	ProfileIDs []string                       `json:"profileIds"`
	Settings   ConfigurationSettingsSelection `json:"settings"`
}

// ConfigurationTargetMapping is explicit; a null destination remains visibly unassigned.
type ConfigurationTargetMapping struct {
	SourceTargetID string  `json:"sourceTargetId"`
	TargetID       *string `json:"targetId"`
}

// ConfigurationExpectation captures the exact preview's CAS and private-catalogue digest.
type ConfigurationExpectation struct {
	PreferenceRevision     int    `json:"preferenceRevision"`
	TemplateRevision       int    `json:"templateRevision"`
	MappingRevision        uint64 `json:"mappingRevision"`
	RetentionCompleteCount int    `json:"retentionCompleteCount"`
	ProfileCount           int    `json:"profileCount"`
	CatalogDigest          string `json:"catalogDigest"`
}

// ConfigurationImportRequest is identical across preview/apply except the explicit expectation.
type ConfigurationImportRequest struct {
	ContractVersion int                          `json:"contractVersion"`
	Bundle          ConfigurationBundle          `json:"bundle"`
	Selection       ConfigurationSelection       `json:"selection"`
	Mappings        []ConfigurationTargetMapping `json:"mappings"`
	Expected        *ConfigurationExpectation    `json:"expected"`
}

// ConfigurationProfilePreview reports blockers without silently dropping selected roots.
type ConfigurationProfilePreview struct {
	SourceProfileID string   `json:"sourceProfileId"`
	Name            string   `json:"name"`
	Selected        bool     `json:"selected"`
	Ready           bool     `json:"ready"`
	Issues          []string `json:"issues"`
}

// ConfigurationCurrentSettings is an explicit before-value projection for the preview diff.
type ConfigurationCurrentSettings struct {
	Appearance             PreferenceAppearance  `json:"appearance"`
	Display                PreferenceDisplay     `json:"display"`
	PreferredProfileRef    *string               `json:"preferredProfileRef"`
	ExclusionTemplate      ConfigurationTemplate `json:"exclusionTemplate"`
	RetentionCompleteCount int                   `json:"retentionCompleteCount"`
	FileTypeMapping        FileTypeMapping       `json:"fileTypeMapping"`
}

// ConfigurationSettingsPreview binds displayed before-values and selected after-groups.
type ConfigurationSettingsPreview struct {
	Current  ConfigurationCurrentSettings   `json:"current"`
	Selected ConfigurationSettingsSelection `json:"selected"`
}

// ConfigurationImportPreview is read-only and grants no source access or future apply guarantee.
type ConfigurationImportPreview struct {
	BundleDigest        string                        `json:"bundleDigest"`
	SelectionDigest     string                        `json:"selectionDigest"`
	Expected            ConfigurationExpectation      `json:"expected"`
	Profiles            []ConfigurationProfilePreview `json:"profiles"`
	UnassignedTargetIDs []string                      `json:"unassignedTargetIds"`
	Settings            ConfigurationSettingsPreview  `json:"settings"`
	Issues              []string                      `json:"issues"`
	CanApply            bool                          `json:"canApply"`
	TasksRequireReview  bool                          `json:"tasksRequireReview"`
}

// ConfigurationCreatedProfile relates a source reference to a newly allocated inactive profile.
type ConfigurationCreatedProfile struct {
	SourceProfileID string `json:"sourceProfileId"`
	ProfileID       string `json:"profileId"`
}

// ConfigurationImportReceipt is compact immutable replay evidence, never the uploaded document.
type ConfigurationImportReceipt struct {
	BundleDigest       string                         `json:"bundleDigest"`
	SelectionDigest    string                         `json:"selectionDigest"`
	Created            []ConfigurationCreatedProfile  `json:"created"`
	SettingsApplied    ConfigurationSettingsSelection `json:"settingsApplied"`
	TasksRequireReview bool                           `json:"tasksRequireReview"`
}
