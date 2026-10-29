package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// Real scans must preserve normal totals when either the recycle root or its nested subtree is replaced.
func TestScopeCompositionRecycleReplacement(t *testing.T) {
	for _, scanPath := range []string{"#recycle", "#recycle/nested"} {
		t.Run(scanPath, func(t *testing.T) {
			m, in, catalog := bootFixture(t)
			share, ctx := catalog.Shares[0], context.Background()
			dir := filepath.Join(share.PhysicalPath, "#recycle", "nested")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "file")
			write := func(size int) {
				t.Helper()
				if err := os.WriteFile(file, make([]byte, size), 0600); err != nil {
					t.Fatal(err)
				}
			}
			scan := func(id string) {
				t.Helper()
				j, err := m.Start(id, "full")
				if err != nil {
					t.Fatal(err)
				}
				if out := terminal(t, m, j.Job.ID); out.SnapshotState != "complete" {
					t.Fatal(out)
				}
			}
			write(5)
			scan(in.Profile.ID)
			req := ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{share.ID, "."}, {share.ID, "#recycle"}, {share.ID, "#recycle/nested"}}}
			initial, err := m.ScopeObservations(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			normal := *initial[0].Bytes
			config := actionConfiguration(in.Profile)
			config.Scope = contracts.Scope{Kind: "folder", TargetIDs: []string{share.ID}, RelativePath: scanPath}
			child, err := m.ProfileAction(ctx, profiles.ActionRequest{ContractVersion: 1, Operation: "create", Configuration: config}, "admin", "recycle-replacement")
			if err != nil {
				t.Fatal(err)
			}
			// Exercise growth, shrinkage and complete emptying through successive real generations.
			for _, size := range []int{10, 2, 0} {
				write(size)
				scan(child.ProfileID)
				rows, err := m.ScopeObservations(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				expected := contracts.Bytes(map[int]string{10: "10", 2: "2", 0: "0"}[size])
				if *rows[0].Bytes != normal || *rows[0].RecycleBytes != expected {
					t.Fatalf("size=%d normal=%s recycle=%s", size, *rows[0].Bytes, *rows[0].RecycleBytes)
				}
				for _, row := range rows[1:] {
					if row.Bytes == nil || *row.Bytes != expected || row.RecycleBytes == nil || *row.RecycleBytes != expected {
						t.Fatalf("selected recycle scope lost its own size: %+v", row)
					}
				}
			}
		})
	}
}
