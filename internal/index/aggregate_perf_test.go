package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"strconv"
)

// BenchmarkDirectoryAggregation isolates the post-scan calculation from insertion and disk sync.
func BenchmarkDirectoryAggregation(b *testing.B) {
	db, err := Open(filepath.Join(b.TempDir(), "index.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = InitializeGeneration(ctx, db, "snapshot"); err != nil {
		b.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	w, err := NewNodeWriter(ctx, tx, "snapshot")
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	rid, err := w.AddRoot("volume", "volume", nil)
	if err != nil {
		b.Fatal(err)
	}
	root, err := w.Insert(rid, 0, []byte("."), "directory", nil, nil, "identity", nil)
	if err != nil {
		b.Fatal(err)
	}
	size := contracts.Bytes("123456789")
	for i := 0; i < 10000; i++ {
		if _, err = w.Insert(rid, root, []byte(fmt.Sprintf("file-%06d.txt", i)), "file", &size, nil, "identity", nil); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a, e := directoryValue(ctx, tx, strconv.FormatInt(root, 10), map[string]string{})
		if e != nil {
			b.Fatal(e)
		}
		if a.Subtree.Logical.KnownBytes != "1234567890000" || a.Subtree.FileCount != "10000" {
			b.Fatal("incorrect aggregate", a)
		}
	}
}

// BenchmarkCompactMetadataBuild measures native tree storage with deterministic sibling names.
// This synthetic shape is not a substitute for measuring the actual NAS dataset.
func BenchmarkCompactMetadataBuild(b *testing.B) {
	for _, count := range []int{10000, 343085} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			for iteration := 0; iteration < b.N; iteration++ {
				db, err := Open(filepath.Join(b.TempDir(), "compact.db"))
				if err != nil {
					b.Fatal(err)
				}
				ctx := context.Background()
				if err = InitializeGeneration(ctx, db, "snapshot"); err != nil {
					b.Fatal(err)
				}
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					b.Fatal(err)
				}
				writer, err := NewNodeWriter(ctx, tx, "snapshot")
				if err != nil {
					b.Fatal(err)
				}
				rid, err := writer.AddRoot("volume", "volume", nil)
				if err != nil {
					b.Fatal(err)
				}
				root, err := writer.Insert(rid, 0, []byte("volume"), "directory", nil, nil, "identity", nil)
				if err != nil {
					b.Fatal(err)
				}
				size := contracts.Bytes("1024")
				for i := 0; i < count; i++ {
					if _, err = writer.Insert(rid, root, []byte(fmt.Sprintf("file-%06d.txt", i)), "file", &size, nil, "identity", nil); err != nil {
						b.Fatal(err)
					}
				}
				writer.Close()
				if err = tx.Commit(); err != nil {
					b.Fatal(err)
				}
				if err = BuildDerivedProjections(ctx, db, DefaultFileTypeMapping()); err != nil {
					b.Fatal(err)
				}
				var pages, pageSize int64
				if err = db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
					b.Fatal(err)
				}
				if err = db.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(pages*pageSize), "database-bytes")
				b.ReportMetric(float64(pages*pageSize)/float64(count+1), "bytes/node")
				db.Close()
			}
		})
	}
}
