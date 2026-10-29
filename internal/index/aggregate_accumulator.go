package index

import (
	"math/big"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// contentAccumulator keeps exact integers mutable until one directory is complete.
// Its size is bounded by the eight file categories, never by the number of children.
type contentAccumulator struct {
	files, directories, special                          big.Int
	logical, logicalUnknown, allocated, allocatedUnknown big.Int
	types                                                map[string]*typeAccumulator
}

// typeAccumulator holds one nonempty category without per-file allocation.
type typeAccumulator struct {
	files, logical, logicalUnknown, allocated, allocatedUnknown big.Int
}

// setDecimal avoids arbitrary-precision parsing for ordinary sizes while preserving exact large sums.
// Callers validate canonical nonnegative decimal input before reaching this arithmetic helper.
func setDecimal(dst *big.Int, value string) {
	if n, err := strconv.ParseUint(value, 10, 64); err == nil {
		dst.SetUint64(n)
		return
	}
	dst.SetString(value, 10)
}

// addDecimal reuses the destination instead of serializing a growing total for every file.
func addDecimal(dst *big.Int, value contracts.Bytes) {
	if value == "0" {
		return
	}
	var n big.Int
	setDecimal(&n, string(value))
	dst.Add(dst, &n)
}

// add consumes an already validated aggregate and preserves arbitrary-precision totals.
func (a *contentAccumulator) add(c contracts.ContentAggregate) {
	addDecimal(&a.files, c.FileCount)
	addDecimal(&a.directories, c.DirectoryCount)
	addDecimal(&a.special, c.SpecialCount)
	addDecimal(&a.logical, c.Logical.KnownBytes)
	addDecimal(&a.logicalUnknown, c.Logical.UnknownFileCount)
	addDecimal(&a.allocated, c.Allocated.KnownBytes)
	addDecimal(&a.allocatedUnknown, c.Allocated.UnknownFileCount)
	for category, v := range c.Types {
		t := a.category(category)
		addDecimal(&t.files, v.FileCount)
		addDecimal(&t.logical, v.Logical.KnownBytes)
		addDecimal(&t.logicalUnknown, v.Logical.UnknownFileCount)
		addDecimal(&t.allocated, v.Allocated.KnownBytes)
		addDecimal(&t.allocatedUnknown, v.Allocated.UnknownFileCount)
	}
}

// category lazily allocates only categories actually represented in the directory.
func (a *contentAccumulator) category(name string) *typeAccumulator {
	if a.types == nil {
		a.types = make(map[string]*typeAccumulator)
	}
	t := a.types[name]
	if t == nil {
		t = &typeAccumulator{}
		a.types[name] = t
	}
	return t
}

// file parses each measured size once and shares it between overall and type totals.
func (a *contentAccumulator) file(category string, logical, allocated *contracts.Bytes) error {
	for _, v := range []*contracts.Bytes{logical, allocated} {
		if v != nil && (v.Validate() != nil || len(*v) > 128) {
			return ErrGeneration
		}
	}
	t := a.category(category)
	one := big.NewInt(1)
	a.files.Add(&a.files, one)
	t.files.Add(&t.files, one)
	for _, m := range []struct {
		value                                  *contracts.Bytes
		total, unknown, typeTotal, typeUnknown *big.Int
	}{
		{logical, &a.logical, &a.logicalUnknown, &t.logical, &t.logicalUnknown},
		{allocated, &a.allocated, &a.allocatedUnknown, &t.allocated, &t.allocatedUnknown},
	} {
		if m.value == nil {
			m.unknown.Add(m.unknown, one)
			m.typeUnknown.Add(m.typeUnknown, one)
		} else {
			var n big.Int
			setDecimal(&n, string(*m.value))
			m.total.Add(m.total, &n)
			m.typeTotal.Add(m.typeTotal, &n)
		}
	}
	return nil
}

// content performs decimal conversion once per completed directory, preserving the public schema.
func (a *contentAccumulator) content() contracts.ContentAggregate {
	metric := func(known, unknown *big.Int) contracts.MetricAggregate {
		return contracts.MetricAggregate{KnownBytes: contracts.Bytes(known.String()), UnknownFileCount: contracts.Bytes(unknown.String())}
	}
	out := contracts.ContentAggregate{FileCount: contracts.Bytes(a.files.String()), DirectoryCount: contracts.Bytes(a.directories.String()), SpecialCount: contracts.Bytes(a.special.String()), Logical: metric(&a.logical, &a.logicalUnknown), Allocated: metric(&a.allocated, &a.allocatedUnknown), Types: map[string]contracts.TypeAggregate{}}
	for category, t := range a.types {
		out.Types[category] = contracts.TypeAggregate{FileCount: contracts.Bytes(t.files.String()), Logical: metric(&t.logical, &t.logicalUnknown), Allocated: metric(&t.allocated, &t.allocatedUnknown)}
	}
	return out
}
