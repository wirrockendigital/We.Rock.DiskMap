package contracts

import "encoding/json"

// SignedBytes represents an explicitly comparable difference, never a file size or reclaim estimate.
type SignedBytes string

// Validate accepts canonical signed decimal integers and excludes negative zero and unbounded payloads.
func (b SignedBytes) Validate() error {
	s := string(b)
	if len(s) == 0 || len(s) > 129 {
		return &ContractError{"INVALID_SIGNED_BYTES"}
	}
	if s[0] == '-' {
		s = s[1:]
		if s == "0" {
			return &ContractError{"INVALID_SIGNED_BYTES"}
		}
	}
	if len(s) > 128 || Bytes(s).Validate() != nil {
		return &ContractError{"INVALID_SIGNED_BYTES"}
	}
	return nil
}

// MarshalJSON preserves differences beyond the JavaScript exact-number range.
func (b SignedBytes) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(string(b))
}

// UnmarshalJSON requires a canonical string; nullable differences use a pointer.
func (b *SignedBytes) UnmarshalJSON(raw []byte) error {
	if len(raw) == 0 || raw[0] != '"' {
		return &ContractError{"INVALID_SIGNED_BYTES"}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	next := SignedBytes(s)
	if err := next.Validate(); err != nil {
		return err
	}
	*b = next
	return nil
}

// AllocatedQualification identifies the empirically verified native method for newly sealed scans.
const AllocatedQualification = "g06-dsm90080-kvmx64-st-blocks512-v1"
