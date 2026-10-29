package mapindex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A broad directory stores its shallow balanced levels first while preserving identities.
func TestPackedAncestorLocality(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Add(Input{ID: "1", Source: 1, Kind: Directory}); err != nil {
		t.Fatal(err)
	}
	for id := uint64(2); id < 10002; id++ {
		if err = b.Add(Input{ID: strconv.FormatUint(id, 10), Source: id, Parent: "1", Kind: File, Bytes: [4]Size{{Lo: 1}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err = b.Finish(); err != nil {
		t.Fatal(err)
	}
	if err = SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "tree.bin"))
	if err != nil {
		t.Fatal(err)
	}
	depths := map[uint64]int{}
	var visit func(uint64, int)
	visit = func(slot uint64, crossings int) {
		depths[slot] = crossings
		at := HeaderBytes + int(slot)*RecordBytes
		r, e := Decode(raw[at : at+RecordBytes])
		if e != nil {
			t.Fatal(e)
		}
		for _, child := range []uint64{r.Left, r.Right} {
			if child == 0 {
				continue
			}
			extra := 1
			visit(child, crossings+extra)
		}
	}
	visit(1, 0)
	for slot := uint64(2); slot <= uint64(len(depths)); slot++ {
		if depths[slot] < depths[slot-1] {
			t.Fatal("deep records precede shallow records", slot)
		}
	}
	reader, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, id := range []string{"2", "5001", "10001"} {
		slot, e := reader.Slot(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		identity, e := reader.Identity(context.Background(), slot)
		if e != nil || identity != id {
			t.Fatal("identity changed", identity, e)
		}
	}
}

// Dirty eviction must preserve addresses when the working set exceeds the fixed page budget.
func TestPackPageEviction(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "pages")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	pages := newPackPages(f, 2)
	for i := 0; i < 20; i++ {
		if err = pages.write([]byte{byte(i + 1)}, int64(i*packPageBytes)); err != nil {
			t.Fatal(err)
		}
	}
	if err = pages.flush(); err != nil {
		t.Fatal(err)
	}
	for i := 19; i >= 0; i-- {
		var value [1]byte
		if err = pages.read(value[:], int64(i*packPageBytes)); err != nil || value[0] != byte(i+1) {
			t.Fatal("address lost", i, value, err)
		}
	}
	if pages.lru.Len() > 2 {
		t.Fatal("page budget exceeded")
	}
}

// Small nested directory trees keep their dependent records inside one transport block.
func TestPackedNestedLocality(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	add := func(id, parent uint64, kind uint8) {
		t.Helper()
		p := ""
		if parent != 0 {
			p = strconv.FormatUint(parent, 10)
		}
		var sizes [4]Size
		if kind == File {
			sizes[0].Lo = 1
		}
		if e := b.Add(Input{ID: strconv.FormatUint(id, 10), Source: id, Parent: p, Kind: kind, Bytes: sizes}); e != nil {
			t.Fatal(e)
		}
	}
	add(1, 0, Directory)
	next := uint64(2)
	directories := []uint64{1}
	for n := 0; n < 3; n++ {
		parent := next
		next++
		add(parent, 1, Directory)
		directories = append(directories, parent)
		for k := 0; k < 3; k++ {
			child := next
			next++
			add(child, parent, Directory)
			directories = append(directories, child)
			for j := 0; j < 5; j++ {
				add(next, child, File)
				next++
			}
		}
	}
	if err = b.Finish(); err != nil {
		t.Fatal(err)
	}
	if err = SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, id := range directories {
		slot, e := r.Slot(context.Background(), strconv.FormatUint(id, 10))
		if e != nil {
			t.Fatal(e)
		}
		record, e := r.Record(slot)
		if e != nil {
			t.Fatal(e)
		}
		pending := []uint64{record.Left}
		var low, high, count uint64
		for len(pending) > 0 {
			at := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if at == 0 {
				continue
			}
			if low == 0 || at < low {
				low = at
			}
			if at > high {
				high = at
			}
			count++
			node, e := r.Record(at)
			if e != nil {
				t.Fatal(e)
			}
			pending = append(pending, node.Left, node.Right)
		}
		if high/RecordsPerBlock != low/RecordsPerBlock || count == 0 {
			t.Fatalf("directory %d contents cross a transport block: %d..%d contains %d records", id, low, high, count)
		}
	}
}
