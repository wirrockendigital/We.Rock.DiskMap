package mapindex

import (
	"encoding/binary"
	"os"
)

// packSlots keeps wide breadth-first frontiers on disk behind a fixed eight-MiB cache.
type packSlots struct {
	pages      *packPages
	head, tail int64
}

func newPackSlots(file *os.File) *packSlots { return &packSlots{pages: newPackPages(file, 2048)} }

// push writes only new queue positions; stale trailing pages are never read after reset.
func (q *packSlots) push(slot uint64) error {
	var raw [8]byte
	binary.LittleEndian.PutUint64(raw[:], slot)
	if err := q.pages.write(raw[:], q.tail); err != nil {
		return err
	}
	q.tail += 8
	return nil
}

// pop advances monotonically and distinguishes an empty frontier from an invalid zero address.
func (q *packSlots) pop() (uint64, bool, error) {
	if q.head == q.tail {
		return 0, false, nil
	}
	var raw [8]byte
	if err := q.pages.read(raw[:], q.head); err != nil {
		return 0, false, err
	}
	q.head += 8
	return binary.LittleEndian.Uint64(raw[:]), true, nil
}

// popLast keeps complete real-directory subtrees adjacent while their local balanced levels use FIFO.
func (q *packSlots) popLast() (uint64, bool, error) {
	if q.head == q.tail {
		return 0, false, nil
	}
	q.tail -= 8
	var raw [8]byte
	if err := q.pages.read(raw[:], q.tail); err != nil {
		return 0, false, err
	}
	return binary.LittleEndian.Uint64(raw[:]), true, nil
}

// reset reuses private scratch capacity without retaining a complete directory frontier in RAM.
func (q *packSlots) reset() { q.head = 0; q.tail = 0 }
