package query

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"strings"
)

// searchTextMatches keeps literal substring semantics unless glob mode is explicit.
// Ordered literal fragments implement '*' without regex backtracking or SQL interpolation.
func searchTextMatches(raw []byte, needle, mode string) bool {
	if mode != "glob" || !strings.Contains(needle, "*") {
		return nativeLiteralContains(raw, needle)
	}
	value := index.SearchFold(searchDisplay(raw))
	parts := strings.Split(needle, "*")
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	value = value[len(parts[0]):]
	for i, part := range parts[1:] {
		if i == len(parts)-2 {
			return strings.HasSuffix(value, part)
		}
		at := strings.Index(value, part)
		if at < 0 {
			return false
		}
		value = value[at+len(part):]
	}
	return true
}
