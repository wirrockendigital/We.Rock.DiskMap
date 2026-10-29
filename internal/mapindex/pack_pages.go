package mapindex

import (
	"container/list"
	"io"
	"os"
)

const packPageBytes = 4096

// packPages bounds random preparation I/O with fixed pages and explicit dirty eviction.
type packPages struct {
	file    *os.File
	limit   int
	entries map[int64]*list.Element
	lru     *list.List
}
type packPage struct {
	offset int64
	data   []byte
	dirty  bool
}

func newPackPages(file *os.File, limit int) *packPages {
	return &packPages{file: file, limit: limit, entries: map[int64]*list.Element{}, lru: list.New()}
}

// page returns a private cache page; reads beyond the final partial page remain zero-filled.
func (p *packPages) page(offset int64) (*packPage, error) {
	base := offset / int64(packPageBytes) * int64(packPageBytes)
	if e := p.entries[base]; e != nil {
		p.lru.MoveToBack(e)
		return e.Value.(*packPage), nil
	}
	var data []byte
	if p.lru.Len() >= p.limit {
		e := p.lru.Front()
		v := e.Value.(*packPage)
		if v.dirty {
			if _, err := p.file.WriteAt(v.data, v.offset); err != nil {
				return nil, err
			}
		}
		delete(p.entries, v.offset)
		p.lru.Remove(e)
		data = v.data
		clear(data)
	}
	if data == nil {
		data = make([]byte, packPageBytes)
	}
	v := &packPage{offset: base, data: data}
	_, err := p.file.ReadAt(v.data, base)
	if err != nil && err != io.EOF {
		return nil, err
	}
	p.entries[base] = p.lru.PushBack(v)
	return v, nil
}

// read and write reject cross-page operations; record and address widths divide the page size.
func (p *packPages) read(raw []byte, offset int64) error {
	v, err := p.page(offset)
	if err != nil {
		return err
	}
	at := int(offset - v.offset)
	if at+len(raw) > len(v.data) {
		return ErrInvalid
	}
	copy(raw, v.data[at:])
	return nil
}
func (p *packPages) write(raw []byte, offset int64) error {
	v, err := p.page(offset)
	if err != nil {
		return err
	}
	at := int(offset - v.offset)
	if at+len(raw) > len(v.data) {
		return ErrInvalid
	}
	copy(v.data[at:], raw)
	v.dirty = true
	return nil
}

// flush writes every dirty address page before publishing derived references.
func (p *packPages) flush() error {
	for e := p.lru.Front(); e != nil; e = e.Next() {
		v := e.Value.(*packPage)
		if v.dirty {
			if _, err := p.file.WriteAt(v.data, v.offset); err != nil {
				return err
			}
			v.dirty = false
		}
	}
	return nil
}
