package query

import (
	"encoding/binary"
	"sort"
)

// EncodeMapCategories stores fixed-size numeric records without names or source paths.
func EncodeMapCategories(c *MapCategories) []byte {
	ids := make([]int64, 0, len(c.Dirs))
	for id := range c.Dirs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	raw := make([]byte, 8+64*len(ids))
	binary.LittleEndian.PutUint64(raw, uint64(len(ids)))
	for i, id := range ids {
		d := c.Dirs[id]
		b := raw[8+i*64 : 8+(i+1)*64]
		binary.LittleEndian.PutUint64(b, uint64(id))
		binary.LittleEndian.PutUint64(b[8:], uint64(d.Parent))
		b[16] = d.Flags
		for k, v := range d.Bytes {
			binary.LittleEndian.PutUint64(b[24+k*8:], v)
		}
		binary.LittleEndian.PutUint64(b[56:], d.Files)
	}
	return raw
}

// DecodeMapCategories rejects malformed dimensions, duplicate IDs, and invalid ancestry.
func DecodeMapCategories(raw []byte) (*MapCategories, error) {
	if len(raw) < 8 {
		return nil, ErrSnapshot
	}
	count := binary.LittleEndian.Uint64(raw)
	if count > 2000000 || uint64(len(raw)) != 8+64*count {
		return nil, ErrSnapshot
	}
	out := &MapCategories{Dirs: make(map[int64]*MapDirectory, int(count))}
	last := int64(0)
	for i := 0; i < int(count); i++ {
		b := raw[8+i*64 : 8+(i+1)*64]
		id := int64(binary.LittleEndian.Uint64(b))
		parent := int64(binary.LittleEndian.Uint64(b[8:]))
		if id <= last || parent < 0 || parent >= id || b[16] > 3 {
			return nil, ErrSnapshot
		}
		d := &MapDirectory{Parent: parent, Flags: b[16], Files: binary.LittleEndian.Uint64(b[56:])}
		if parent != 0 && (out.Dirs[parent] == nil || d.Flags&out.Dirs[parent].Flags != out.Dirs[parent].Flags) {
			return nil, ErrSnapshot
		}
		for k := range d.Bytes {
			d.Bytes[k] = binary.LittleEndian.Uint64(b[24+k*8:])
		}
		out.Dirs[id] = d
		last = id
	}
	return out, nil
}
