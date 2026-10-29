package scanner

import (
	"context"
	"errors"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Real SQLITE_FULL must not count the bytes of a row whose insertion failed.
func TestProgressSQLFullBoundary(t *testing.T) {
	p, m := coverageFixture(t)
	for i := 0; i < 300; i++ {
		if e := os.WriteFile(filepath.Join(m.Roots[0].PhysicalPath, fmt.Sprintf("%03d-", i)+strings.Repeat("x", 90)), []byte("data"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	// Measure the current schema separately because ScanManifest initializes its own empty database.
	probe, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer probe.Close()
	if e = index.InitializeGeneration(context.Background(), probe, "snapshot"); e != nil {
		t.Fatal(e)
	}
	var schemaPages int
	if e = probe.QueryRow(`PRAGMA page_count`).Scan(&schemaPages); e != nil {
		t.Fatal(e)
	}
	// Leave enough room for real rows, but not the entire fixture, regardless of schema growth.
	if _, e = db.Exec(fmt.Sprintf(`PRAGMA max_page_count=%d`, schemaPages+8)); e != nil {
		t.Fatal(e)
	}
	var sample Progress
	called := false
	ctx := WithProgress(context.Background(), func(p Progress) { sample = p; called = true })
	_, e = ScanManifest(ctx, db, "snapshot", p, m, index.DefaultFileTypeMapping())
	var coded interface{ Code() int }
	if !errors.As(e, &coded) || coded.Code() != 13 {
		t.Fatalf("expected actual SQLite capacity failure: %v", e)
	}
	if !called || sample.Files == 0 || sample.KnownLogicalBytes != strconv.FormatUint(sample.Files*4, 10) || sample.Entries != sample.Files+sample.Directories+sample.Specials {
		t.Fatalf("inconsistent final sample %+v", sample)
	}
}

// Progress formatting must not scale with entry count when observers are throttled or absent.
func TestProgressFormatsOnlyDeliveredSamples(t *testing.T) {
	formats, deliveries := 0, 0
	sample := func() Progress { formats++; return Progress{Entries: 42} }
	absent := progressReporter(context.Background())
	absent(sample, false)
	absent(sample, true)
	if formats != 0 {
		t.Fatal("formatted progress without an observer")
	}
	report := progressReporter(WithProgress(context.Background(), func(p Progress) {
		deliveries++
		if p.Entries != 42 {
			t.Fatal("lost measured progress")
		}
	}))
	report(sample, false)
	for i := 0; i < 1000; i++ {
		report(sample, false)
	}
	report(sample, true)
	if formats != 2 || deliveries != 2 {
		t.Fatalf("formats=%d deliveries=%d", formats, deliveries)
	}
}
