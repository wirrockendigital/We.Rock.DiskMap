package scanner

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

// Replay real directory batches to verify unique counts, subtree pruning and changed-path gaps.
func TestRepeatedDirectoryEntries(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "changed"}[changed], func(t *testing.T) {
			p, manifest := coverageFixture(t)
			for _, root := range manifest.Roots {
				dir := filepath.Join(root.Binding.Path, "nested")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "leaf"), []byte("abc"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			db, err := index.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			pending := map[*os.File][]os.DirEntry{}
			result, err := scanManifestWithOperations(context.Background(), db, "snapshot", p, manifest, index.DefaultFileTypeMapping(), walkOperations{
				inspect: inspectEntry, directory: openDirectory,
				readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
					if entries, ok := pending[f]; ok {
						delete(pending, f)
						if changed {
							// Change only the fixture's root file after its first observation.
							for _, root := range manifest.Roots {
								info, _ := f.Stat()
								original, _ := os.Stat(root.Binding.Path)
								if os.SameFile(info, original) {
									if e := os.WriteFile(filepath.Join(root.Binding.Path, "file"), []byte("replacement"), 0600); e != nil {
										t.Fatal(e)
									}
								}
							}
						}
						return entries, io.EOF
					}
					entries, e := f.ReadDir(n)
					pending[f] = append([]os.DirEntry(nil), entries...)
					return entries, e
				},
			})
			wantErrors := uint64(0)
			if changed {
				wantErrors = 2
			}
			if err != nil || result.Entries != 8 || result.Bytes != "14" || result.DuplicateEntries != 6 || result.Errors != wantErrors {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			var count int
			if e := db.QueryRow("SELECT count(*) FROM nodes").Scan(&count); e != nil || count != 8 {
				t.Fatal(count, e)
			}
			// The stored aggregate must remain exact despite replayed directory and file records.
			var sum int
			if e := db.QueryRow("SELECT sum(CAST(json_extract(a.aggregate_json,'$.subtree.logical.knownBytes') AS INTEGER)) FROM directory_aggregates a JOIN nodes n ON n.id=a.node_id WHERE n.parent_id IS NULL").Scan(&sum); e != nil || sum != 14 {
				t.Fatal(sum, e)
			}
		})
	}
}
