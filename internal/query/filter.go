package query

import (
	"math/big"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Canonical normalizes immutable AND predicates without inventing unknown values.
func (f Filter) Canonical() (Filter, error) {
	// Schema13 does not record timestamps; reject unsupported predicates explicitly.
	if f.ModifiedFrom != nil || f.ModifiedTo != nil {
		return f, ErrRequest
	}
	f.Categories = append([]string{}, f.Categories...)
	f.ObjectKinds = append([]string{}, f.ObjectKinds...)
	for _, list := range [][]string{f.Categories, f.ObjectKinds} {
		sort.Strings(list)
		for i, s := range list {
			if i > 0 && s == list[i-1] {
				return f, ErrRequest
			}
		}
	}
	if len(f.Categories) > 8 || len(f.ObjectKinds) > 2 || len(f.Text) > 512 || !utf8.ValidString(f.Text) {
		return f, ErrRequest
	}
	for _, c := range f.Categories {
		if !categories[c] {
			return f, ErrRequest
		}
	}
	for _, k := range f.ObjectKinds {
		if k != "file" && k != "directory" {
			return f, ErrRequest
		}
	}
	for _, p := range []**contracts.Bytes{&f.MinSize, &f.MaxSize} {
		if *p != nil {
			v := **p
			if len(v) > 128 || v.Validate() != nil {
				return f, ErrRequest
			}
			*p = &v
		}
	}
	if f.MinSize != nil && f.MaxSize != nil && compareBytes(*f.MinSize, *f.MaxSize) > 0 {
		return f, ErrRequest
	}
	if f.Field == "" {
		f.Field = "both"
	}
	if f.Field != "name" && f.Field != "path" && f.Field != "both" {
		return f, ErrRequest
	}
	return f, nil
}

// compareBytes compares validated decimal values without floating-point conversion.
func compareBytes(a, b contracts.Bytes) int {
	x, _ := new(big.Int).SetString(string(a), 10)
	y, _ := new(big.Int).SetString(string(b), 10)
	return x.Cmp(y)
}

// HasPredicates identifies actual restrictions; display mode is deliberately not a predicate.
func (f Filter) HasPredicates() bool {
	return len(f.Categories) > 0 || len(f.ObjectKinds) > 0 || f.MinSize != nil || f.MaxSize != nil || f.ModifiedFrom != nil || f.ModifiedTo != nil || f.Text != ""
}

// matches applies all predicates to one object's recorded metadata and selected size.
// Direct directory matching is separate from any retained ancestor or subtree inclusion.
func (f Filter) matches(n contracts.Node, rawName []byte, size *contracts.Bytes, mapping contracts.FileTypeMapping) bool {
	if len(f.ObjectKinds) > 0 {
		found := false
		for _, k := range f.ObjectKinds {
			found = found || k == n.Kind
		}
		if !found {
			return false
		}
	}
	if len(f.Categories) > 0 && (n.Kind != "file" || !selected(f, category(rawName, mapping))) {
		return false
	}
	if f.MinSize != nil || f.MaxSize != nil {
		if size == nil {
			return false
		}
		if f.MinSize != nil && compareBytes(*size, *f.MinSize) < 0 {
			return false
		}
		if f.MaxSize != nil && compareBytes(*size, *f.MaxSize) > 0 {
			return false
		}
	}
	if f.Text != "" {
		text := index.SearchFold(f.Text)
		if !((f.Field != "path" && strings.Contains(index.SearchFold(n.OriginalName), text)) || (f.Field != "name" && strings.Contains(index.SearchFold(n.RelativePath), text))) {
			return false
		}
	}
	return true
}
