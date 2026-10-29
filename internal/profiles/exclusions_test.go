package profiles

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"strings"
	"testing"
	"time"
)

// TestExclusionLanguage covers component boundaries, depth, object kind and lossless Unix names.
func TestExclusionLanguage(t *testing.T) {
	cases := []struct {
		kind, rule, path string
		dir, want        bool
	}{
		{"path", "cache", "cache/a", false, true}, {"path", "cache", "cache2/a", false, false},
		{"path", "a.tmp", "nested/a.tmp", false, false}, {"pattern", "*.tmp", "nested/a.tmp", false, true},
		{"pattern", "a/*.tmp", "a/deep/f.tmp", false, false}, {"pattern", "a/**/*.tmp", "a/f.tmp", false, true},
		{"pattern", "a/**/*.tmp", "a/deep/f.tmp", false, true}, {"pattern", "**/cache/", "cache/a", false, true},
		{"pattern", "cache/", "cache", false, false}, {"pattern", "cache/", "x/cache", true, true},
		{"pattern", "**", ".", true, false}, {"path", "*", "*", false, true},
		{"pattern", "Grüße?.tmp", "deep/Grüße😀.tmp", false, true}, {"pattern", "?.tmp", "é.tmp", false, true},
		{"pattern", "é.tmp", "e\u0301.tmp", false, false}, {"pattern", "*.TMP", "a.tmp", false, false},
		{"pattern", "bad�.txt", "bad\xff.txt", false, false}, {"pattern", "bad?.txt", "bad\xff.txt", false, true},
		{"pattern", "bad�.txt", "bad�.txt", false, true}, {"pattern", "**/*.tmp", "a.tmp", false, true},
	}
	for _, c := range cases {
		t.Run(c.rule+"/"+c.path, func(t *testing.T) {
			m, e := CompileExclusions([]contracts.Exclusion{{Kind: c.kind, Value: c.rule}})
			if e != nil || m.Match(c.path, c.dir) != c.want {
				t.Fatalf("match got %v error %v", m, e)
			}
		})
	}
	// Default rules cannot silently omit DSM metadata, hidden files or recycle directories.
	m, _ := CompileExclusions(nil)
	for _, p := range []string{".hidden", "#recycle/a", "@eaDir/a"} {
		if m.Match(p, false) {
			t.Fatal(p)
		}
	}
}

// TestExclusionSyntax rejects ambiguous language before persistence and preserves canonical copies.
func TestExclusionSyntax(t *testing.T) {
	for _, rule := range []string{"", "/a", "../a", "a/../b", "./a", "a//b", "a/**x", "[ab]", "{a,b}", "!a", "a\\b", "a\n", "a\x00", "\xff"} {
		if _, err := CompileExclusions([]contracts.Exclusion{{Kind: "pattern", Value: rule}}); !errors.Is(err, ErrExclusionSyntax) {
			t.Fatal(rule, err)
		}
	}
	if _, err := CompileExclusions([]contracts.Exclusion{{Kind: "path", Value: strings.Repeat("a", 4097)}}); err == nil {
		t.Fatal("unbounded")
	}
	template := []contracts.Exclusion{{Kind: "pattern", Value: "*.tmp"}}
	local := []contracts.Exclusion{{Kind: "path", Value: "cache"}, {Kind: "pattern", Value: "*.tmp"}}
	rules, err := EffectiveExclusions(template, local)
	if err != nil || len(rules) != 2 {
		t.Fatal(rules, err)
	}
	template[0].Value = "*"
	local[0].Value = "changed"
	raw, _ := json.Marshal(rules)
	var imported []contracts.Exclusion
	if json.Unmarshal(raw, &imported) != nil {
		t.Fatal("roundtrip")
	}
	a, _ := CompileExclusions(rules)
	b, _ := CompileExclusions(imported)
	for _, p := range []string{"cache/a", "keep", "x/a.tmp"} {
		if a.Match(p, false) != b.Match(p, false) {
			t.Fatal(p)
		}
	}
	if a.Match("keep", false) {
		t.Fatal("template aliased")
	}
}

// TestExclusionCPUAndCancellationBounds exercises deep adversarial rules without unbounded work.
func TestExclusionCPUAndCancellationBounds(t *testing.T) {
	rules := []contracts.Exclusion{{Kind: "pattern", Value: strings.Repeat("**/", 200) + "never"}}
	matcher, err := CompileExclusions(rules)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	matched, err := matcher.MatchContext(context.Background(), strings.Repeat("a/", 255)+"leaf", false)
	if matched || !errors.Is(err, ErrExclusionBudget) || time.Since(started) > time.Second {
		t.Fatal(matched, err, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = matcher.MatchContext(ctx, "a", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// Match is a test-only shorthand; production callers must handle MatchContext errors.
func (s *ExclusionSet) Match(relative string, directory bool) bool {
	matched, err := s.MatchContext(context.Background(), relative, directory)
	if err != nil {
		panic(err)
	}
	return matched
}

// matcherCancelContext deterministically revokes work after matching has already entered its loops.
type matcherCancelContext struct {
	context.Context
	calls int
}

func (c *matcherCancelContext) Err() error {
	c.calls++
	if c.calls >= 4 {
		return context.Canceled
	}
	return nil
}

// TestExclusionMidMatchCancellation proves the CPU loop observes cancellation before budget exhaustion.
func TestExclusionMidMatchCancellation(t *testing.T) {
	matcher, err := CompileExclusions([]contracts.Exclusion{{Kind: "pattern", Value: strings.Repeat("**/", 200) + "never"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &matcherCancelContext{Context: context.Background()}
	if _, err = matcher.MatchContext(ctx, strings.Repeat("a/", 255)+"leaf", false); !errors.Is(err, context.Canceled) || ctx.calls != 4 {
		t.Fatal(ctx.calls, err)
	}
}
