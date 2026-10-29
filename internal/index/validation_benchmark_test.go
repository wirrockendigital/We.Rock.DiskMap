package index

import (
	"context"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
	"path/filepath"
	"testing"
)

// BenchmarkGenerationValidation isolates independent validation from scanning and online aggregation.
func BenchmarkGenerationValidation(b *testing.B) {
	for n := 0; n < b.N; n++ {
		b.StopTimer()
		ctx := context.Background()
		db, err := Open(filepath.Join(b.TempDir(), "generation.db"))
		if err != nil {
			b.Fatal(err)
		}
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
		for d := 0; d < 1000; d++ {
			parent, e := writer.Insert(rid, root, []byte(fmt.Sprintf("d%d", d)), "directory", nil, nil, "identity", nil)
			if e != nil {
				b.Fatal(e)
			}
			for f := 0; f < 100; f++ {
				if _, e = writer.Insert(rid, parent, []byte(fmt.Sprintf("f%d.txt", f)), "file", &size, nil, "identity", nil); e != nil {
					b.Fatal(e)
				}
			}
		}
		writer.Close()
		if err = tx.Commit(); err != nil {
			b.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO coverage VALUES('volume','complete',0)`); err != nil {
			b.Fatal(err)
		}
		if err = BuildDerivedProjections(ctx, db, DefaultFileTypeMapping()); err != nil {
			b.Fatal(err)
		}
		h := GenerationHeader{AggregateVersion: AggregateVersion, FileTypeMapping: DefaultFileTypeMapping(), SnapshotID: "snapshot", JobID: "job", Profile: contracts.Profile{ID: "profile", Revision: 1}, Manifest: scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: "volumes", Roots: []scopemanifest.ManifestRoot{{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: "volume", VolumeID: "volume", Kind: "volume"}}}}, StartedAt: "2026-10-09T00:00:00Z", EndedAt: "2026-10-09T00:01:00Z", ScannerVersion: ScannerVersion, SchemaVersion: GenerationSchema, Methods: []string{"descriptor-no-follow-stat"}, State: "complete", Entries: 101001, LogicalBytes: "102400000"}
		h.Manifest.Provenance = scopemanifest.ExplicitProvenance(h.Manifest, true)
		b.StartTimer()
		err = FinalizeGeneration(ctx, db, h)
		b.StopTimer()
		if err != nil {
			b.Fatal(err)
		}
		db.Close()
	}
}
