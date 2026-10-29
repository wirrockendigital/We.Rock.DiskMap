package index

import (
	"strings"
	"unicode"
)

// SearchFold uses Unicode simple case equivalence without inventing normalization or multi-rune expansion.
func SearchFold(s string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < lowest {
				lowest = next
			}
		}
		return lowest
	}, s)
}
