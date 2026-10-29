package contracts

// OnboardingConfiguration is deliberately incomplete and never an executable profile request.
type OnboardingConfiguration struct {
	Name             *string         `json:"name,omitempty"`
	Scope            *Scope          `json:"scope,omitempty"`
	Active           *bool           `json:"active,omitempty"`
	Exclusions       *[]Exclusion    `json:"exclusions,omitempty"`
	ResourceMode     *string         `json:"resourceMode,omitempty"`
	Limits           *ResourceLimits `json:"limits,omitempty"`
	FreshnessSeconds *int            `json:"freshnessSeconds,omitempty"`
}

// OnboardingDraft stores user choices only, without permissions or session material.
type OnboardingDraft struct {
	Step          int                      `json:"step"`
	Language      string                   `json:"language"`
	Configuration *OnboardingConfiguration `json:"configuration"`
	ProfileID     *string                  `json:"profileId"`
	UpdateChoice  string                   `json:"updateChoice"`
}
