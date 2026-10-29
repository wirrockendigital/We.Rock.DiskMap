package scanner

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"path/filepath"
	"testing"
)

// Native metadata traversal prunes optional categories without turning intentional exclusions into gaps.
func TestScanCategoryOptions(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		t.Run(map[bool]string{false: "included", true: "excluded"}[excluded], func(t *testing.T) {
			p, m := coverageFixture(t)
			root := m.Roots[0].PhysicalPath
			for _, name := range []string{"#recycle/old.bin", "@eaDir/thumb.bin", "nested/.DS_Store", "nested/.SMBfixture"} {
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if excluded {
				for _, value := range []string{"#recycle/", "@eaDir/", ".DS_Store", ".SMB*"} {
					p.Exclusions = append(p.Exclusions, contracts.Exclusion{Kind: "pattern", Value: value})
				}
			}
			db, err := index.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			result, err := ScanManifest(context.Background(), db, "snapshot", p, m, index.DefaultFileTypeMapping())
			if err != nil || result.Errors != 0 {
				t.Fatal(result, err)
			}
			var files int
			if err = db.QueryRow(`SELECT count(*) FROM nodes WHERE kind='file'`).Scan(&files); err != nil {
				t.Fatal(err)
			}
			want := 6
			if excluded {
				want = 2
			}
			if files != want {
				t.Fatalf("files=%d want=%d", files, want)
			}
			// The scan owns only its index; all four category source files must remain intact.
			for _, name := range []string{"#recycle/old.bin", "@eaDir/thumb.bin", "nested/.DS_Store", "nested/.SMBfixture"} {
				b, e := os.ReadFile(filepath.Join(root, name))
				if e != nil || string(b) != "test" {
					t.Fatal("source changed", e)
				}
			}
		})
	}
}
