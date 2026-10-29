// Package filestation maps indexed objects through the current authoritative share registry.
package filestation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

var ErrUnavailable = errors.New("FILE_STATION_UNAVAILABLE")

// Target carries only a current logical share path and an optional explicit recovery notice.
type Target struct {
	FileStationPath string `json:"fileStationPath"`
	Kind            string `json:"kind"`
	ParentPath      string `json:"parentPath,omitempty"`
	NoticeCode      string `json:"noticeCode,omitempty"`
}

// IdentityLookup reads an indexed ancestor's private identity; it never traverses source contents.
type IdentityLookup func(relative string) (string, error)

// Resolve rejects substitutions, links and mounts before returning a bounded, metadata-only target.
func Resolve(ctx context.Context, binding scanner.RootBinding, relative, kind, identity string, shares map[string]string, lookup IdentityLookup) (Target, error) {
	var empty Target
	if len(relative) > 4096 || strings.Count(relative, "/") > 255 || len(shares) > 1024 || security.RelativeBytes(relative) != nil || !utf8.ValidString(relative) || (kind != "file" && kind != "directory") {
		return empty, ErrUnavailable
	}
	current, shareName, sharePath, err := resolveMapping(ctx, binding, relative, shares)
	if err != nil {
		return empty, err
	}
	info, err := scanner.InspectBoundPath(current, relative)
	selected := relative
	notice := ""
	if err != nil {
		// A permission error, replacement or symlink must never become a missing-file recovery.
		if kind != "file" || !errors.Is(err, os.ErrNotExist) || lookup == nil {
			return empty, ErrUnavailable
		}
		selected = filepath.Dir(relative)
		for depth := 0; ; depth++ {
			if depth >= 256 || ctx.Err() != nil {
				return empty, ErrUnavailable
			}
			expected, e := lookup(selected)
			if e != nil {
				return empty, ErrUnavailable
			}
			info, e = scanner.InspectBoundPath(current, selected)
			if e == nil {
				if !info.IsDir() || !scanner.SameObject(info, expected) {
					return empty, ErrUnavailable
				}
				break
			}
			if !errors.Is(e, os.ErrNotExist) || selected == "." {
				return empty, ErrUnavailable
			}
			selected = filepath.Dir(selected)
		}
		kind = "directory"
		notice = "FILE_REMOVED_PARENT_AVAILABLE"
	} else if !scanner.SameObject(info, identity) || (kind == "file" && !info.Mode().IsRegular()) || (kind == "directory" && !info.IsDir()) {
		return empty, ErrUnavailable
	}
	physical := filepath.Join(current.Path, selected)
	rel, e := filepath.Rel(sharePath, physical)
	if e != nil || security.RelativeBytes(rel) != nil {
		return empty, ErrUnavailable
	}
	// Recheck the mapped object from the share anchor too; lexical containment alone is not authority.
	root, shareBinding, e := scanner.OpenBoundRoot(sharePath)
	if e != nil {
		return empty, ErrUnavailable
	}
	root.Close()
	mappedInfo, e := scanner.InspectBoundPath(shareBinding, rel)
	if e != nil || !os.SameFile(info, mappedInfo) || !scanner.SameObject(mappedInfo, scanner.ObjectIdentity(info)) {
		return empty, ErrUnavailable
	}
	mapped := "/" + shareName
	if rel != "." {
		mapped += "/" + filepath.ToSlash(rel)
	}
	out := Target{FileStationPath: mapped, Kind: kind, NoticeCode: notice}
	if kind == "file" {
		out.ParentPath = filepath.ToSlash(filepath.Dir(mapped))
	}
	return out, nil
}

// resolveMapping permits a renamed root only when its mounted filesystem identity still matches.
func resolveMapping(ctx context.Context, binding scanner.RootBinding, relative string, shares map[string]string) (scanner.RootBinding, string, string, error) {
	current := binding
	valid := false
	root, observed, err := scanner.OpenBoundRoot(binding.Path)
	if err == nil {
		root.Close()
		valid = observed == binding
	}
	name, path := "", ""
	for candidate, p := range shares {
		if ctx.Err() != nil {
			return binding, "", "", ErrUnavailable
		}
		if candidate == "" || candidate == "." || candidate == ".." || strings.ContainsAny(candidate, "/\\\x00") || strings.HasPrefix(candidate, "@") || !utf8.ValidString(candidate) {
			return binding, "", "", ErrUnavailable
		}
		// Read only share roots; never enumerate unrelated share contents.
		r, b, e := scanner.OpenBoundRoot(p)
		if e != nil {
			continue
		}
		r.Close()
		if b.Device == binding.Device && b.Inode == binding.Inode && b.MountID == binding.MountID {
			if name != "" && path != p {
				return binding, "", "", ErrUnavailable
			}
			current = b
			valid = true
			name = candidate
			path = p
		}
	}
	if !valid {
		return binding, "", "", ErrUnavailable
	}
	physical := filepath.Join(current.Path, relative)
	// Volume profiles can map a descendant only through a real registry root and subsequent descriptor checks.
	for candidate, p := range shares {
		if physical == p || strings.HasPrefix(physical, p+string(filepath.Separator)) {
			if len(p) > len(path) {
				name, path = candidate, p
			} else if len(p) == len(path) && candidate != name {
				return binding, "", "", ErrUnavailable
			}
		}
	}
	if name == "" {
		return binding, "", "", ErrUnavailable
	}
	return current, name, path, nil
}
