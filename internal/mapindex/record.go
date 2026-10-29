// Package mapindex stores immutable, block-addressable visualization trees.
package mapindex

import (
	"encoding/binary"
	"errors"
	"math/big"
	"math/bits"
)

const (
	Version         = 1
	RecordBytes     = 128
	BlockBytes      = 256 << 10
	RecordsPerBlock = BlockBytes / RecordBytes
	HeaderBytes     = 4096
	Compound        = 0
	File            = 1
	Directory       = 2
	Special         = 3
	Virtual         = 4
)

var ErrInvalid = errors.New("INVALID_MAP_INDEX")

// Size is an exact unsigned 128-bit aggregate; conversion to float is a renderer concern.
type Size struct{ Lo, Hi uint64 }

// ParseSize rejects noncanonical values and explicit overflow instead of rounding totals.
func ParseSize(s string) (Size, error) {
	if s == "" || len(s) > 39 || len(s) > 1 && s[0] == '0' {
		return Size{}, ErrInvalid
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return Size{}, ErrInvalid
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.BitLen() > 128 {
		return Size{}, ErrInvalid
	}
	lo := n.Uint64()
	n.Rsh(n, 64)
	return Size{lo, n.Uint64()}, nil
}

// String preserves exact decimal values at JSON boundaries.
func (s Size) String() string {
	n := new(big.Int).SetUint64(s.Hi)
	n.Lsh(n, 64)
	n.Or(n, new(big.Int).SetUint64(s.Lo))
	return n.String()
}

// Add checks both words for overflow.
func (s Size) Add(t Size) (Size, error) {
	lo, c := bits.Add64(s.Lo, t.Lo, 0)
	hi, c := bits.Add64(s.Hi, t.Hi, c)
	if c != 0 {
		return Size{}, ErrInvalid
	}
	return Size{lo, hi}, nil
}

// Less provides the stable unsigned ordering used by the layout balancer.
func (s Size) Less(t Size) bool { return s.Hi < t.Hi || s.Hi == t.Hi && s.Lo < t.Lo }

// Record separates real ancestry from synthetic layout edges. Slot zero is always absent.
type Record struct {
	Source, Parent, Left, Right uint64
	Kind, Flags, Presence       uint8
	Depth, MaxDepth             uint16
	Files, Unknown              uint64
	Bytes                       [4]Size
}

// Total returns the exact weight across disjoint category buckets.
func (r Record) Total() (Size, error) {
	var out Size
	for _, v := range r.Bytes {
		var err error
		out, err = out.Add(v)
		if err != nil {
			return Size{}, err
		}
	}
	return out, nil
}

// Encode uses a fixed little-endian format shared with the browser decoder.
func (r Record) Encode(b []byte) {
	clear(b[:RecordBytes])
	for i, v := range []uint64{r.Source, r.Parent, r.Left, r.Right} {
		binary.LittleEndian.PutUint64(b[i*8:], v)
	}
	b[32], b[33], b[38] = r.Kind, r.Flags, r.Presence
	binary.LittleEndian.PutUint16(b[34:], r.Depth)
	binary.LittleEndian.PutUint16(b[36:], r.MaxDepth)
	binary.LittleEndian.PutUint64(b[40:], r.Files)
	binary.LittleEndian.PutUint64(b[48:], r.Unknown)
	for i, v := range r.Bytes {
		binary.LittleEndian.PutUint64(b[56+i*16:], v.Lo)
		binary.LittleEndian.PutUint64(b[64+i*16:], v.Hi)
	}
}

// Decode rejects unknown flags and reserved bytes before exposing a record.
func Decode(b []byte) (Record, error) {
	if len(b) != RecordBytes || b[32] > Virtual || b[33] > 3 || b[38] > 3 {
		return Record{}, ErrInvalid
	}
	for _, i := range []int{39, 120, 121, 122, 123, 124, 125, 126, 127} {
		if b[i] != 0 {
			return Record{}, ErrInvalid
		}
	}
	r := Record{Source: binary.LittleEndian.Uint64(b), Parent: binary.LittleEndian.Uint64(b[8:]), Left: binary.LittleEndian.Uint64(b[16:]), Right: binary.LittleEndian.Uint64(b[24:]), Kind: b[32], Flags: b[33], Presence: b[38], Depth: binary.LittleEndian.Uint16(b[34:]), MaxDepth: binary.LittleEndian.Uint16(b[36:]), Files: binary.LittleEndian.Uint64(b[40:]), Unknown: binary.LittleEndian.Uint64(b[48:])}
	for i := range r.Bytes {
		r.Bytes[i] = Size{binary.LittleEndian.Uint64(b[56+i*16:]), binary.LittleEndian.Uint64(b[64+i*16:])}
	}
	if _, err := r.Total(); err != nil {
		return Record{}, err
	}
	return r, nil
}
