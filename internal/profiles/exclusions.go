package profiles

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// ExclusionSyntaxVersion identifies the small, case-sensitive V1 rule language.
const ExclusionSyntaxVersion = 1

// ErrExclusionSyntax never embeds private paths or rule values in diagnostics.
var ErrExclusionSyntax = errors.New("INVALID_EXCLUSION_SYNTAX")

// ExclusionSyntaxError identifies a rejected rule without exposing its contents.
type ExclusionSyntaxError struct {
	RuleIndex int    `json:"ruleIndex"`
	Code      string `json:"code"`
}

func (e *ExclusionSyntaxError) Error() string { return e.Code }
func (e *ExclusionSyntaxError) Unwrap() error { return ErrExclusionSyntax }

// exclusionRule stores validated relative components, never an operating-system path.
type exclusionRule struct {
	kind      string
	parts     []string
	directory bool
	basename  bool
}

// ExclusionSet is immutable after compilation and shared by scanner and index preview.
type ExclusionSet struct{ rules []exclusionRule }

// CompileExclusions accepts bounded explicit paths and simple globs without shell expansion.
func CompileExclusions(input []contracts.Exclusion) (*ExclusionSet, error) {
	out := &ExclusionSet{}
	fail := func(i int, code string) (*ExclusionSet, error) { return nil, &ExclusionSyntaxError{i, code} }
	if len(input) > 256 {
		return fail(-1, "EXCLUSION_LIMIT")
	}
	total := 0
	for i, e := range input {
		total += len(e.Value)
		if total > 16384 || len(e.Value) > 4096 {
			return fail(i, "EXCLUSION_LIMIT")
		}
		if e.Value == "" || !utf8.ValidString(e.Value) || (e.Kind != "path" && e.Kind != "pattern") {
			return fail(i, "EXCLUSION_INVALID_RULE")
		}
		if strings.HasPrefix(e.Value, "/") {
			return fail(i, "EXCLUSION_RELATIVE_REQUIRED")
		}
		for _, r := range e.Value {
			if unicode.IsControl(r) {
				return fail(i, "EXCLUSION_CONTROL_CHARACTER")
			}
		}
		directory := strings.HasSuffix(e.Value, "/")
		value := strings.TrimSuffix(e.Value, "/")
		parts := strings.Split(value, "/")
		for _, part := range parts {
			if part == "" || part == "." || part == ".." {
				return fail(i, "EXCLUSION_INVALID_COMPONENT")
			}
			if e.Kind == "pattern" && (strings.ContainsAny(part, "[]{}\\!") || strings.Contains(part, "**") && part != "**") {
				return fail(i, "EXCLUSION_UNSUPPORTED_PATTERN")
			}
		}
		out.rules = append(out.rules, exclusionRule{e.Kind, parts, directory, e.Kind == "pattern" && len(parts) == 1})
	}
	return out, nil
}

// EffectiveExclusions copies a template into the profile; later template edits have no effect.
func EffectiveExclusions(template, local []contracts.Exclusion) ([]contracts.Exclusion, error) {
	all := append(append([]contracts.Exclusion{}, template...), local...)
	if _, err := CompileExclusions(all); err != nil {
		return nil, err
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].Value < all[j].Value
	})
	out := []contracts.Exclusion{}
	for _, e := range all {
		if len(out) == 0 || out[len(out)-1] != e {
			out = append(out, e)
		}
	}
	return out, nil
}

// ErrExclusionBudget rejects excessive matching work rather than silently keeping a node.
var ErrExclusionBudget = errors.New("EXCLUSION_MATCH_BUDGET")

// matchBudget bounds CPU work inside wildcard loops, independently from database cancellation.
type matchBudget struct {
	ctx       context.Context
	remaining int
}

func (b *matchBudget) step() error {
	b.remaining--
	if b.remaining < 0 {
		return ErrExclusionBudget
	}
	if b.remaining%256 == 0 {
		return b.ctx.Err()
	}
	return nil
}

// MatchContext includes excluded ancestors so preview and pruned traversal agree.
// The root is retained as a structural anchor even when every child is excluded.
func (s *ExclusionSet) MatchContext(ctx context.Context, relative string, directory bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if relative == "." || relative == "" {
		return false, nil
	}
	budget := &matchBudget{ctx: ctx, remaining: 1000000}
	parts := strings.Split(relative, "/")
	for end := 1; end <= len(parts); end++ {
		isDir := end < len(parts) || directory
		for _, r := range s.rules {
			if err := budget.step(); err != nil {
				return false, err
			}
			if r.directory && !isDir {
				continue
			}
			candidate := parts[:end]
			if r.basename {
				matched, err := globComponent(r.parts[0], parts[end-1], budget)
				if matched || err != nil {
					return matched, err
				}
				continue
			}
			if r.kind == "path" {
				if len(candidate) != len(r.parts) {
					continue
				}
				equal := true
				for i := range candidate {
					if candidate[i] != r.parts[i] {
						equal = false
						break
					}
				}
				if equal {
					return true, nil
				}
			} else {
				matched, err := globPath(r.parts, candidate, budget)
				if matched || err != nil {
					return matched, err
				}
			}
		}
	}
	return false, nil
}

// globPath uses bounded dynamic programming; ** consumes whole components including zero.
func globPath(pattern, value []string, budget *matchBudget) (bool, error) {
	previous := make([]bool, len(value)+1)
	previous[0] = true
	for _, part := range pattern {
		next := make([]bool, len(value)+1)
		if part == "**" {
			next[0] = previous[0]
		}
		for i := 1; i <= len(value); i++ {
			if err := budget.step(); err != nil {
				return false, err
			}
			if part == "**" {
				next[i] = previous[i] || next[i-1]
			} else if previous[i-1] {
				matched, err := globComponent(part, value[i-1], budget)
				if err != nil {
					return false, err
				}
				next[i] = matched
			}
		}
		previous = next
	}
	return previous[len(value)], nil
}

// pathUnits keeps malformed Unix bytes distinct from a genuine Unicode replacement character.
func pathUnits(s string) []rune {
	out := make([]rune, 0, len(s))
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			r = -256 - rune(s[0])
		}
		out = append(out, r)
		s = s[n:]
	}
	return out
}

// globComponent applies * and ? to original units, preserving case and normalization forms.
func globComponent(pattern, value string, budget *matchBudget) (bool, error) {
	p, v := pathUnits(pattern), pathUnits(value)
	pi, vi, star, retry := 0, 0, -1, 0
	for vi < len(v) {
		if err := budget.step(); err != nil {
			return false, err
		}
		if pi < len(p) && (p[pi] == '?' || p[pi] == v[vi]) {
			pi++
			vi++
			continue
		}
		if pi < len(p) && p[pi] == '*' {
			star = pi
			pi++
			retry = vi
			continue
		}
		if star >= 0 {
			retry++
			vi = retry
			pi = star + 1
			continue
		}
		return false, nil
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p), nil
}
