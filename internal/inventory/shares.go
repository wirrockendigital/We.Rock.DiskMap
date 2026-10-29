package inventory

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// RegisteredShare is authoritative DSM metadata, never an inferred directory registration.
// A provider must deliver a complete list and prove the UUID and exact physical mapping.
type RegisteredShare struct {
	UUID             string
	Name             string
	VolumeMountPath  string
	PhysicalPath     string
	FileStationPath  string
	Encryption       *int
	IdentityEvidence string
}

// ShareObservation separates package-account readability from administrative metadata.
type ShareObservation struct {
	VolumeID string
	Exists   bool
	Readable bool
}

// ShareProbe verifies current source binding without traversing source contents.
type ShareProbe func(context.Context, RegisteredShare) (ShareObservation, error)

// ShareResult preserves absent identities and makes uncertain mappings explicit.
type ShareResult struct {
	Shares []contracts.Share `json:"shares"`
	Issues []Issue           `json:"issues"`
}

// readableShares publishes only roots qualified and opened by the package account.
// DSM ACL mode and SMB/FTP listing policy must not hide locally readable shares.
func readableShares(shares []contracts.Share) []contracts.Share {
	visible := make([]contracts.Share, 0, len(shares))
	for _, share := range shares {
		if share.Readable && share.State == "reachable" {
			visible = append(visible, share)
		}
	}
	return visible
}

// CollectShares accepts a complete verified registry; provider failure must never be passed as empty.
func CollectShares(ctx context.Context, registered []RegisteredShare, volumes []contracts.Volume, previous []contracts.Share, probe ShareProbe) (ShareResult, error) {
	result := ShareResult{Shares: []contracts.Share{}, Issues: []Issue{}}
	if len(registered) > 1024 || len(previous) > 1024 || probe == nil {
		return result, errShareRegistry
	}
	byVolume := map[string][]contracts.Volume{}
	for _, v := range volumes {
		byVolume[v.MountPath] = append(byVolume[v.MountPath], v)
	}
	uuids := map[string]int{}
	names := map[string]int{}
	paths := map[string]int{}
	for _, r := range registered {
		if !uuidPattern.MatchString(r.UUID) || r.UUID == "00000000-0000-0000-0000-000000000000" || r.IdentityEvidence == "" || !shareName(r.Name) || !volumePath.MatchString(r.VolumeMountPath) || !cleanAbsolute(r.PhysicalPath) || !cleanAbsolute(r.FileStationPath) || r.FileStationPath != "/"+r.Name {
			return result, errors.New("INVALID_SHARE_REGISTRY")
		}
		uuids[r.UUID]++
		names[strings.ToLower(r.Name)]++
		paths[r.PhysicalPath]++
	}
	present := map[string]bool{}
	for _, r := range registered {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		id := "share-" + r.UUID
		present[id] = true
		// Duplicate persistent identities cannot safely identify even an unavailable entry.
		if uuids[r.UUID] != 1 {
			return result, errors.New("DUPLICATE_SHARE_ID")
		}
		s := contracts.Share{ID: id, Name: r.Name, PhysicalPath: r.PhysicalPath, State: "unverified", IdentityEvidence: r.IdentityEvidence}
		if r.Encryption != nil {
			switch *r.Encryption {
			case 0:
				s.Encrypted = boolPointer(false)
				s.Locked = boolPointer(false)
			case 1:
				s.Encrypted = boolPointer(true)
				s.Locked = boolPointer(true)
			case 2:
				s.Encrypted = boolPointer(true)
				s.Locked = boolPointer(false)
			}
		}
		issue := ""
		candidates := byVolume[r.VolumeMountPath]
		if len(candidates) != 1 || candidates[0].State != "reachable" {
			issue = "SHARE_VOLUME_UNVERIFIED"
		} else {
			s.VolumeID = candidates[0].ID
		}
		if names[strings.ToLower(r.Name)] != 1 || paths[r.PhysicalPath] != 1 {
			issue = "AMBIGUOUS_SHARE_MAPPING"
		}
		if s.Encrypted == nil {
			issue = "SHARE_ENCRYPTION_UNVERIFIED"
		}
		if issue != "" {
			result.Issues = append(result.Issues, Issue{issue, r.PhysicalPath})
		} else if *s.Locked {
			s.State = "locked"
		} else {
			observed, err := probe(ctx, r)
			if err != nil || observed.VolumeID != s.VolumeID {
				result.Issues = append(result.Issues, Issue{"SHARE_BINDING_UNVERIFIED", r.PhysicalPath})
			} else if !observed.Exists {
				s.State = "missing"
			} else if !observed.Readable {
				s.State = "unreadable"
			} else {
				s.State = "reachable"
				s.Readable = true
				fs := r.FileStationPath
				s.FileStationPath = &fs
			}
		}
		result.Shares = append(result.Shares, s)
	}
	for _, s := range previous {
		if !strings.HasPrefix(s.ID, "share-") || !uuidPattern.MatchString(strings.TrimPrefix(s.ID, "share-")) {
			return result, errors.New("INVALID_PREVIOUS_SHARE")
		}
		if present[s.ID] {
			continue
		}
		present[s.ID] = true
		s.State = "missing"
		s.Readable = false
		s.FileStationPath = nil
		s.Encrypted = nil
		s.Locked = nil
		result.Shares = append(result.Shares, s)
	}
	sort.Slice(result.Shares, func(i, j int) bool { return result.Shares[i].ID < result.Shares[j].ID })
	sort.Slice(result.Issues, func(i, j int) bool { return result.Issues[i].MountPath < result.Issues[j].MountPath })
	return result, nil
}

// MapSharePath maps one registered share identity bidirectionally without guessing volume prefixes.
// A caller must revalidate the selected share binding before launching File Station.
func MapSharePath(share contracts.Share, value string, toFileStation bool) (string, error) {
	if share.State != "reachable" || !share.Readable || share.FileStationPath == nil || !cleanAbsolute(value) {
		return "", errors.New("FILE_STATION_UNAVAILABLE")
	}
	from, to := share.PhysicalPath, *share.FileStationPath
	if !toFileStation {
		from, to = to, from
	}
	if !cleanAbsolute(from) || !cleanAbsolute(to) {
		return "", errors.New("FILE_STATION_UNAVAILABLE")
	}
	if value == from {
		return to, nil
	}
	if !strings.HasPrefix(value, from+"/") {
		return "", errors.New("OUTSIDE_SHARE")
	}
	return to + value[len(from):], nil
}

// cleanAbsolute preserves percent signs and Unicode while rejecting path ambiguity and controls.
func cleanAbsolute(value string) bool {
	return utf8.ValidString(value) && strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "/" && strings.IndexFunc(value, unicode.IsControl) < 0
}

// shareName accepts one exact registry component without permitting path traversal.
func shareName(value string) bool {
	return value != "" && value != "." && value != ".." && utf8.ValidString(value) && !strings.ContainsAny(value, "/\\") && strings.IndexFunc(value, unicode.IsControl) < 0
}

// boolPointer retains the distinction between false and unobserved metadata.
func boolPointer(value bool) *bool { return &value }
