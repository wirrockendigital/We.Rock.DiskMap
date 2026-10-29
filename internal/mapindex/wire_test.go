package mapindex

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Verify full and short blocks independently across the canonical and byte-plane representations.
func TestCompressedBytePlanes(t *testing.T) {
	dir := fixture(t, 2500)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for block := uint64(0); block < r.Blocks; block++ {
		compressed, digest, err := r.CompressedBlock(block)
		if err != nil {
			t.Fatal(err)
		}
		gz, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			t.Fatal(err)
		}
		planes, err := io.ReadAll(gz)
		gz.Close()
		if err != nil {
			t.Fatal(err)
		}
		canonical, _, err := r.Block(block)
		if err != nil {
			t.Fatal(err)
		}
		restored := make([]byte, len(planes))
		rows := len(planes) / RecordBytes
		for row := 0; row < rows; row++ {
			for column := 0; column < RecordBytes; column++ {
				restored[row*RecordBytes+column] = planes[column*rows+row]
			}
		}
		sum := sha256.Sum256(restored)
		if !bytes.Equal(restored, canonical) || hex.EncodeToString(sum[:]) != digest {
			t.Fatal("wire reconstruction differs")
		}
	}
	// A change to compressed bytes must fail before any damaged payload can leave the server.
	f, err := os.OpenFile(filepath.Join(dir, "blocks.gz"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var value [1]byte
	if _, err = f.ReadAt(value[:], 20); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 1
	if _, err = f.WriteAt(value[:], 20); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, _, err = r.CompressedBlock(0); err == nil {
		t.Fatal("corrupt compressed block accepted")
	}
}

// Reseal an explicitly supplied copied layout for wire-format browser qualification.
func TestSealBrowserFixture(t *testing.T) {
	dir := os.Getenv("DISKMAP_SEAL_FIXTURE")
	if dir == "" {
		t.Skip("opt-in wire fixture")
	}
	if err := SealBlocks(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
}
