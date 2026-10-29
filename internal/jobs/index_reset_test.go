package jobs

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"os"
	"path/filepath"
	"testing"
)

// Subtree reset removes only indexed descendants, preserves siblings and never changes source files.
func TestIndexResetSubtreeAndAll(t *testing.T) {
	m, in, catalog := bootFixture(t)
	source := catalog.Shares[0].PhysicalPath
	for _, name := range []string{"a", "b"} {
		if e := os.MkdirAll(filepath.Join(source, name), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(source, name, "file"), []byte("hello"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	job, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	old := terminal(t, m, job.Job.ID)
	if old.SnapshotState != "complete" {
		t.Fatal(old)
	}
	request := IndexResetRequest{ContractVersion: 1, Confirmed: true, Scope: &contracts.Scope{Kind: "folder", TargetIDs: []string{catalog.Shares[0].ID}, RelativePath: "a"}}
	receipt, e := m.ResetIndex(context.Background(), request, "admin", "reset-test-1")
	if e != nil {
		t.Fatal(e)
	}
	if receipt.Generations != 1 || !receipt.Completed {
		t.Fatal(receipt)
	}
	same, e := m.ResetIndex(context.Background(), request, "admin", "reset-test-1")
	if e != nil || same != receipt {
		t.Fatal(same, e)
	}
	got, e := m.ScopeObservations(context.Background(), ScopeObservationRequest{ContractVersion: 1, Scopes: []ObservedScope{{catalog.Shares[0].ID, "b"}, {catalog.Shares[0].ID, "a"}}})
	if e != nil {
		t.Fatal(e)
	}
	if got[0].Bytes == nil || *got[0].Bytes != "5" || got[0].State != "complete" {
		t.Fatal(got)
	}
	if got[1].State != "unscanned" || got[1].Bytes != nil {
		t.Fatal("reset not unknown", got[1])
	}
	all, e := m.ResetIndex(context.Background(), IndexResetRequest{ContractVersion: 1, All: true, Confirmed: true}, "admin", "reset-test-2")
	if e != nil || !all.Completed {
		t.Fatal(all, e)
	}
	for _, name := range []string{"a", "b"} {
		b, e := os.ReadFile(filepath.Join(source, name, "file"))
		if e != nil || string(b) != "hello" {
			t.Fatal("source changed", e)
		}
	}
}
