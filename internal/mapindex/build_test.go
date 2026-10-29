package mapindex

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fixture includes overlapping categories, exact large integers, unknowns and an empty directory.
func fixture(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, in := range []Input{{ID: "nas", Kind: Virtual}, {ID: "1", Parent: "nas", Source: 1, Kind: Directory}, {ID: "2", Parent: "1", Source: 2, Kind: Directory, Flags: 2}, {ID: "3", Parent: "2", Source: 3, Kind: Directory, Flags: 1}, {ID: "4", Parent: "1", Source: 4, Kind: Directory}} {
		if err = b.Add(in); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		parent := "1"
		if i%2 == 1 {
			parent = "3"
		}
		s, _ := ParseSize("9007199254740993")
		if err = b.Add(Input{ID: strconv.Itoa(i + 10), Parent: parent, Source: uint64(i + 10), Kind: File, Bytes: [4]Size{s}, Unknown: i == 0}); err != nil {
			t.Fatal(err)
		}
	}
	if err = b.Finish(); err != nil {
		t.Fatal(err)
	}
	if err = SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestExactTree checks every real leaf once and conserves all four category buckets.
func TestExactTree(t *testing.T) {
	dir := fixture(t, 31)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	root, err := r.Slot(context.Background(), "nas")
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.Record(root)
	if err != nil {
		t.Fatal(err)
	}
	if n.Files != 31 || n.Unknown != 1 || n.Bytes[0].String() != "144115188075855888" || n.Bytes[3].String() != "135107988821114895" {
		t.Fatalf("wrong totals: %+v", n)
	}
	seen := map[uint64]bool{}
	var walk func(uint64) Record
	walk = func(slot uint64) Record {
		if seen[slot] {
			t.Fatal("duplicate/cyclic edge")
		}
		seen[slot] = true
		x, e := r.Record(slot)
		if e != nil {
			t.Fatal(e)
		}
		var totals [4]Size
		for _, child := range []uint64{x.Left, x.Right} {
			if child == 0 {
				continue
			}
			v := walk(child)
			for k := range totals {
				totals[k], e = totals[k].Add(v.Bytes[k])
				if e != nil {
					t.Fatal(e)
				}
			}
		}
		if x.Left != 0 && x.Bytes != totals {
			t.Fatal("weight not conserved")
		}
		return x
	}
	walk(root)
}

// TestCorruption refuses an altered record even when its dimensions remain valid.
func TestCorruption(t *testing.T) {
	dir := fixture(t, 8)
	f, e := os.OpenFile(filepath.Join(dir, "tree.bin"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteAt([]byte{99}, HeaderBytes+RecordBytes+56)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	r, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if _, _, e = r.Block(0); e == nil {
		t.Fatal("corrupt block accepted")
	}
}

// TestCancellation never produces a readable finished artifact after interruption.
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dir := t.TempDir()
	b, e := NewBuilder(ctx, dir)
	if e != nil {
		t.Fatal(e)
	}
	cancel()
	if e = b.Add(Input{ID: "r", Kind: Directory}); e == nil {
		t.Fatal("cancel ignored")
	}
	b.Close()
	if r, e := Open(dir); e == nil {
		r.Close()
		t.Fatal("unfinished index accepted")
	}
}

// TestSizes validates exact values above JavaScript's integer range and explicit overflow.
func TestSizes(t *testing.T) {
	for _, s := range []string{"0", "9007199254740993", "340282366920938463463374607431768211455"} {
		v, e := ParseSize(s)
		if e != nil || v.String() != s {
			t.Fatal(s, e)
		}
	}
	for _, s := range []string{"", "01", "-1", "1e3", "340282366920938463463374607431768211456"} {
		if _, e := ParseSize(s); e == nil {
			t.Fatal(s)
		}
	}
	v, _ := ParseSize("340282366920938463463374607431768211455")
	if _, e := v.Add(Size{Lo: 1}); e == nil {
		t.Fatal("overflow")
	}
}

// BenchmarkWideDirectory exercises the disk-backed queue without retaining sibling objects.
func BenchmarkWideDirectory(b *testing.B) {
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		w, e := NewBuilder(context.Background(), dir)
		if e != nil {
			b.Fatal(e)
		}
		if e = w.Add(Input{ID: "r", Kind: Directory}); e != nil {
			b.Fatal(e)
		}
		for j := 0; j < 100000; j++ {
			if e = w.Add(Input{ID: strconv.Itoa(j), Parent: "r", Kind: File, Source: uint64(j + 1), Bytes: [4]Size{{Lo: uint64(j%97 + 1)}}}); e != nil {
				b.Fatal(e)
			}
		}
		if e = w.Finish(); e != nil {
			b.Fatal(e)
		}
		w.Close()
	}
}

// Zero-byte category presence survives balancing independently of visible byte weights.
func TestEmptyCategoriesAndDeepAncestry(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Add(Input{ID: "root", Kind: Virtual}); err != nil {
		t.Fatal(err)
	}
	parent := "root"
	for i := 0; i < 500; i++ {
		id := strconv.Itoa(i + 1)
		if err = b.Add(Input{ID: id, Parent: parent, Source: uint64(i + 1), Kind: Directory}); err != nil {
			t.Fatal(err)
		}
		parent = id
	}
	if err = b.Add(Input{ID: "zero", Parent: parent, Kind: File, Flags: 3}); err != nil {
		t.Fatal(err)
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
	root, err := r.Record(1)
	if err != nil {
		t.Fatal(err)
	}
	if root.Presence != 3 || root.MaxDepth != 501 || root.Files != 1 {
		t.Fatal(root)
	}
}

// Layout edges cannot acquire disconnected children, duplicate IDs or cyclic ancestry.
func TestInvalidParentAndDuplicate(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBuilder(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.Add(Input{ID: "1", Parent: "missing", Kind: Directory}); err == nil {
		t.Fatal("missing parent accepted")
	}
	if err = b.Add(Input{ID: "1", Kind: Directory}); err != nil {
		t.Fatal(err)
	}
	if err = b.Add(Input{ID: "1", Parent: "1", Kind: Directory}); err == nil {
		t.Fatal("duplicate accepted")
	}
}

// Corrupt lookup addresses must not retarget a real node to another valid record.
func TestLookupIdentityCorruption(t *testing.T) {
	dir := fixture(t, 8)
	db, err := sql.Open("sqlite", filepath.Join(dir, "lookup.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE nodes SET id='999' WHERE id='10'`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err = r.Slot(context.Background(), "999"); err == nil {
		t.Fatal("lookup identity corruption accepted")
	}
}

// Compressed corruption and invalid offsets are rejected before bytes reach a client.
func TestCompressedCorruption(t *testing.T) {
	for _, name := range []string{"blocks.gz", "offsets.bin"} {
		t.Run(name, func(t *testing.T) {
			dir := fixture(t, 8)
			f, err := os.OpenFile(filepath.Join(dir, name), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteAt([]byte{255, 255, 255, 255, 255, 255, 255, 255}, 0)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			r, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if _, _, err = r.CompressedBlock(0); err == nil {
				t.Fatal("compressed corruption accepted")
			}
		})
	}
}

// A held old descriptor must not authorize invalidation of a newly published artifact.
func TestReplacementIdentity(t *testing.T) {
	dir := fixture(t, 8)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.MatchesDirectory(dir) {
		t.Fatal("original identity mismatch")
	}
	path := filepath.Join(dir, "tree.bin")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if r.MatchesDirectory(dir) {
		t.Fatal("replacement mistaken for old corrupt artifact")
	}
}
