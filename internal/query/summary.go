package query

import (
	"math/big"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Arithmetic stays decimal and rejects invalid persisted values instead of silently rounding.
func zero() Summary { return Summary{"0", "0", "0", "0", "0"} }
func arithmetic(a, b contracts.Bytes, subtract bool) (contracts.Bytes, error) {
	if len(a) > 128 || len(b) > 128 || a.Validate() != nil || b.Validate() != nil {
		return "", ErrSnapshot
	}
	// Identities apply only after both decimal operands have been fully validated.
	if b == "0" {
		return a, nil
	}
	if !subtract && a == "0" {
		return b, nil
	}
	x, _ := new(big.Int).SetString(string(a), 10)
	y, _ := new(big.Int).SetString(string(b), 10)
	if subtract {
		x.Sub(x, y)
	} else {
		x.Add(x, y)
	}
	if x.Sign() < 0 {
		return "", ErrSnapshot
	}
	// Reuse the exact decimal result for the bound check and return value.
	value := x.String()
	if len(value) > 128 {
		return "", ErrSnapshot
	}
	return contracts.Bytes(value), nil
}
func merge(a, b Summary, subtract bool) (Summary, error) {
	av := []*contracts.Bytes{&a.KnownBytes, &a.UnknownFileCount, &a.FileCount, &a.DirectoryCount, &a.SpecialCount}
	bv := []contracts.Bytes{b.KnownBytes, b.UnknownFileCount, b.FileCount, b.DirectoryCount, b.SpecialCount}
	for i := range av {
		v, e := arithmetic(*av[i], bv[i], subtract)
		if e != nil {
			return a, e
		}
		*av[i] = v
	}
	return a, nil
}
func count(s Summary) contracts.Bytes {
	v, _ := arithmetic(s.FileCount, s.DirectoryCount, false)
	v, _ = arithmetic(v, s.SpecialCount, false)
	return v
}
func selected(f Filter, c string) bool {
	if len(f.Categories) == 0 {
		return true
	}
	for _, v := range f.Categories {
		if v == c {
			return true
		}
	}
	return false
}

// category uses the sealed mapping and recorded raw filename, including its exact Unicode semantics.
func category(raw []byte, m contracts.FileTypeMapping) string {
	suffix, ok := index.RecordedExtension(raw)
	if !ok {
		return "otherUnknown"
	}
	for _, a := range m.Assignments {
		if a.Extension == suffix {
			return a.Category
		}
	}
	return "otherUnknown"
}

// content filters stored regular-file aggregates while retaining structural directory context.
func content(c contracts.ContentAggregate, r Request) (Summary, error) {
	s := zero()
	s.DirectoryCount = c.DirectoryCount
	if len(r.Filter.Categories) == 0 {
		s.FileCount = c.FileCount
		s.SpecialCount = c.SpecialCount
		v := c.Logical
		if r.Metric == "allocated" {
			v = c.Allocated
		}
		s.KnownBytes = v.KnownBytes
		s.UnknownFileCount = v.UnknownFileCount
		return merge(zero(), s, false)
	}
	for _, key := range r.Filter.Categories {
		v, ok := c.Types[key]
		if !ok {
			continue
		}
		metric := v.Logical
		if r.Metric == "allocated" {
			metric = v.Allocated
		}
		piece := zero()
		piece.FileCount = v.FileCount
		piece.KnownBytes = metric.KnownBytes
		piece.UnknownFileCount = metric.UnknownFileCount
		var err error
		s, err = merge(s, piece, false)
		if err != nil {
			return s, err
		}
	}
	return merge(zero(), s, false)
}
