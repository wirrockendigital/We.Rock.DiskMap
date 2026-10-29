package jobs

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Preparation and replay use the same owner-bound lease as every other generation reader.
func TestMapIndexReplayAndLease(t *testing.T) {
	m, base := admissionFixture(t)
	in := MapRequest{ContractVersion: base.ContractVersion, ProfileID: base.ProfileID, ViewID: base.ViewID, Query: base.Query}
	ctx := context.Background()
	var manifest MapManifest
	for attempt := 0; attempt < 200; attempt++ {
		var err error
		manifest, err = m.ReadMapManifest(ctx, "admin", in)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if manifest.Status != "ready" || manifest.Root == 0 || manifest.Count == 0 {
		t.Fatal(manifest)
	}
	in.IndexID = manifest.IndexID
	in.Blocks = []uint64{manifest.Root / 2048}
	first, err := m.ReadMapChunks(ctx, "admin", in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.ReadMapChunks(ctx, "admin", in)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatal("immutable replay changed", err)
	}
	if string(first[:8]) != "WRDCH003" || binary.LittleEndian.Uint32(first[40:]) != 1 {
		t.Fatal("invalid frame")
	}
	// Corrupt only the derived compressed data; the immutable scan and lease must survive repair.
	root, err := m.mapIndexDirectory()
	if err != nil {
		t.Fatal(err)
	}
	packed, err := os.OpenFile(filepath.Join(root, manifest.IndexID, "blocks.gz"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = packed.WriteAt([]byte{0}, 0)
	packed.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.ReadMapChunks(ctx, "admin", in); err == nil {
		t.Fatal("corrupt packed block accepted")
	}
	manifestIn := in
	manifestIn.Blocks = nil
	manifestIn.IndexID = ""
	for attempt := 0; attempt < 200; attempt++ {
		fresh, e := m.ReadMapManifest(ctx, "admin", manifestIn)
		if e == nil && fresh.Status == "ready" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	repaired, err := m.ReadMapChunks(ctx, "admin", in)
	if err != nil || !bytes.Equal(repaired, first) {
		t.Fatal("derived artifact did not recover identically", err)
	}
	if _, err = m.ReadMapChunks(ctx, "different-owner", in); err == nil {
		t.Fatal("owner bypass")
	}
	bad := in
	bad.IndexID = strings.Repeat("f", 64)
	if _, err = m.ReadMapChunks(ctx, "admin", bad); err == nil {
		t.Fatal("index binding bypass")
	}
	bad = in
	bad.Blocks = []uint64{0, 0}
	if _, err = m.ReadMapChunks(ctx, "admin", bad); err == nil {
		t.Fatal("duplicate admission")
	}
	bad = in
	bad.Blocks = []uint64{manifest.Count + 1}
	if _, err = m.ReadMapChunks(ctx, "admin", bad); err == nil {
		t.Fatal("out of bounds admission")
	}
	bad = in
	bad.Query.RootID = "foreign-root"
	if _, err = m.ReadMapChunks(ctx, "admin", bad); err == nil {
		t.Fatal("unknown root admission")
	}
}

// Retained entries must not starve cleanup of later abandoned stages.
func TestMapIndexCleanupAdvances(t *testing.T) {
	m, _ := admissionFixture(t)
	root, err := m.mapIndexDirectory()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 70; i++ {
		if err = os.Mkdir(filepath.Join(root, fmt.Sprintf("%064x", i)), 0700); err != nil {
			t.Fatal(err)
		}
	}
	// A stale bound artifact sorts after the retained prefix.
	key := strings.Repeat("f", 64)
	dir := filepath.Join(root, key)
	if err = os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "binding.json"), []byte(`{"snapshot":"abcdef0123456789abcdef0123456789","key":"`+key+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	m.cleanMapIndexes()
	m.cleanMapIndexes()
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup cursor did not reach stale artifact", err)
	}
}
