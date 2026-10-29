package query

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"strings"
)

// ASCII equivalence is exactly the ASCII subset of SearchFold. Non-ASCII and invalid Unix names
// use the existing canonical display and Unicode simple-fold semantics without normalization.
func nativeLiteralContains(raw []byte, folded string) bool {
	ascii := true
	for _, b := range raw {
		if b >= 128 {
			ascii = false
			break
		}
	}
	if ascii {
		for i := 0; i < len(folded); i++ {
			if folded[i] >= 128 {
				ascii = false
				break
			}
		}
	}
	if !ascii {
		return strings.Contains(index.SearchFold(searchDisplay(raw)), folded)
	}
	for start := 0; start+len(folded) <= len(raw); start++ {
		match := true
		for i := 0; i < len(folded); i++ {
			b := raw[start+i]
			if b >= 'a' && b <= 'z' {
				b -= 'a' - 'A'
			}
			if b != folded[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
