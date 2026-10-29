package security

import (
	"regexp"
	"strings"
	"testing"
)

// TestIDGrammarParity checks every byte in every position against the previous public grammar.
func TestIDGrammarParity(t *testing.T) {
	oracle := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
	check := func(s string) {
		t.Helper()
		if ValidID(s) != oracle.MatchString(s) {
			t.Fatalf("grammar changed for %q", s)
		}
	}
	for _, length := range []int{0, 1, 2, 127, 128, 129} {
		check(strings.Repeat("a", length))
		for pos := 0; pos < length; pos++ {
			v := []byte(strings.Repeat("a", length))
			for n := 0; n < 256; n++ {
				v[pos] = byte(n)
				check(string(v))
			}
		}
	}
	for _, s := range []string{"ä", "aä", "a\n", "a\x00", "_a", "-a", "a_-", "a😀"} {
		check(s)
	}
}
