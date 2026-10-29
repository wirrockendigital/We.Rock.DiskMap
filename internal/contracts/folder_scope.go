package contracts

import (
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ValidFolderPath accepts bounded canonical relative directories, never traversal or absolute paths.
func ValidFolderPath(value string, allowRoot bool) bool {
	if value == "." {
		return allowRoot
	}
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, "\\") || strings.Count(value, "/") > 255 {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." || part == "." || part == "" {
			return false
		}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// ValidFolderScope keeps the additive path field exclusive to one selected share.
func ValidFolderScope(s Scope) bool {
	if s.Kind == "folder" {
		return len(s.TargetIDs) == 1 && ValidFolderPath(s.RelativePath, false)
	}
	return s.RelativePath == ""
}
