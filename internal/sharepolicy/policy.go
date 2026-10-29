// Package sharepolicy parses the qualified DSM visibility policy without credentials.
package sharepolicy

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrPolicy denies publication when the native policy cannot be qualified.
var ErrPolicy = errors.New("SHARE_POLICY_UNAVAILABLE")

// Rights contains only registered path and the explicit encryption flag, never key material.
type Rights struct {
	Path           string
	Encrypted      bool
	Locked         bool
	HideUnreadable bool
	ListDisabled   bool
	ACL            bool
	SkipSMB        bool
}

// Parse reads the build-qualified INI contract; absent encryption means plaintext.
func Parse(reader io.Reader) (map[string]Rights, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	if err != nil || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return nil, ErrPolicy
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 65536)
	out := map[string]Rights{}
	seen := map[string]bool{}
	fields := map[string]bool{}
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, ErrPolicy
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			fold := strings.ToLower(section)
			if !shareName(section) || seen[fold] {
				return nil, ErrPolicy
			}
			seen[fold] = true
			out[section] = Rights{}
			fields = map[string]bool{}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if !ok || section == "" || fields[key] {
			return nil, ErrPolicy
		}
		fields[key] = true
		item := out[section]
		switch key {
		case "path":
			if !cleanAbsolute(value) {
				return nil, ErrPolicy
			}
			item.Path = value
		case "encryption":
			if value != "yes" && value != "show" {
				return nil, ErrPolicy
			}
			item.Encrypted = true
			item.Locked = value == "show"
		case "hide unreadable", "ftp disable list", "edit synoacl", "skip smb perm":
			if value != "yes" && value != "no" {
				return nil, ErrPolicy
			}
			switch key {
			case "hide unreadable":
				item.HideUnreadable = value == "yes"
			case "ftp disable list":
				item.ListDisabled = value == "yes"
			case "edit synoacl":
				item.ACL = value == "yes"
			case "skip smb perm":
				item.SkipSMB = value == "yes"
			}
		case "include", "copy":
			return nil, ErrPolicy
		}
		out[section] = item
	}
	if scanner.Err() != nil {
		return nil, ErrPolicy
	}
	for _, r := range out {
		if r.Path == "" {
			return nil, ErrPolicy
		}
	}
	return out, nil
}

// cleanAbsolute preserves percent signs and Unicode while rejecting path ambiguity and controls.
func cleanAbsolute(value string) bool {
	return utf8.ValidString(value) && strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "/" && strings.IndexFunc(value, unicode.IsControl) < 0
}

// shareName accepts one exact registry component without permitting path traversal.
func shareName(value string) bool {
	return value != "" && value != "." && value != ".." && utf8.ValidString(value) && !strings.ContainsAny(value, "/\\") && strings.IndexFunc(value, unicode.IsControl) < 0
}
