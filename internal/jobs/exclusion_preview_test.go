package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestExclusionPreviewAndScan proves one matcher drives source pruning and index-only prediction.
func TestExclusionPreviewAndScan(t *testing.T) {
	m, in, source := fixture(t)
	ctx := context.Background()
	for _, dir := range []string{"cache", "cache2", "deep/more", "@eaDir", "#recycle"} {
		if err := os.MkdirAll(filepath.Join(source, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cache/hidden", "cache2/keep", "deep/one.tmp", "deep/more/Grüße😀.tmp", "@eaDir/keep", "#recycle/keep", ".hidden"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	request := ExclusionPreviewRequest{ContractVersion: 1, ProfileID: in.Profile.ID, TemplateExclusions: []contracts.Exclusion{{Kind: "pattern", Value: "**/*.tmp"}}, Exclusions: []contracts.Exclusion{{Kind: "path", Value: "cache"}}}
	empty, err := m.PreviewExclusions(ctx, request)
	if err != nil || !empty.Valid || empty.SnapshotID != nil {
		t.Fatal(empty, err)
	}
	start, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	base := terminal(t, m, start.Job.ID)
	if base.SnapshotState != "complete" {
		t.Fatal(base)
	}
	// Moving the entire source away makes any source enumeration fail; preview still uses only SQLite.
	moved := source + "-away"
	if err = os.Rename(source, moved); err != nil {
		t.Fatal(err)
	}
	preview, err := m.PreviewExclusions(ctx, request)
	if restore := os.Rename(moved, source); restore != nil {
		t.Fatal(restore)
	}
	if err != nil || !preview.Valid || preview.Truncated || preview.Matched != 4 || preview.SnapshotID == nil {
		t.Fatal(preview, err)
	}
	// Copy the preview's effective rules into a new profile revision, preserving the baseline generation.
	in.Profile.Revision = 2
	in.Profile.Exclusions = preview.EffectiveExclusions
	if err = m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	start, err = m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	after := terminal(t, m, start.Job.ID)
	baseCount, _ := strconv.Atoi(base.Job.EntriesVisited)
	afterCount, _ := strconv.Atoi(after.Job.EntriesVisited)
	if after.SnapshotState != "complete" || afterCount != baseCount-preview.Matched {
		t.Fatal(base, after)
	}
	db, close, err := m.generationDB(*after.Job.ResultSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, *after.Job.ResultSnapshotID)
	if err != nil || len(header.Profile.Exclusions) != 2 {
		t.Fatal(header, err)
	}
	for _, record := range fixtureNodes(t, db) {
		if p := string(record.Path); p == "cache" || p == "cache/hidden" || p == "deep/one.tmp" {
			t.Fatal("excluded node retained", p)
		}
	}
	// Effective rule serialization is the same representation retained by the immutable snapshot.
	encoded, _ := json.Marshal(preview.EffectiveExclusions)
	stored, _ := json.Marshal(header.Profile.Exclusions)
	if string(encoded) != string(stored) {
		t.Fatal("rules changed")
	}
	invalid := request
	invalid.Exclusions = []contracts.Exclusion{{Kind: "pattern", Value: "../*"}}
	bad, err := m.PreviewExclusions(ctx, invalid)
	if err != nil || bad.Valid || bad.SyntaxError == nil {
		t.Fatal(bad, err)
	}
}

// TestExclusionPreviewLimits never treats a sampled index as a complete source prediction.
func TestExclusionPreviewLimits(t *testing.T) {
	m, in, source := fixture(t)
	for i := 0; i < 1005; i++ {
		if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("f%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, run.Job.ID)
	out, err := m.PreviewExclusions(context.Background(), ExclusionPreviewRequest{ContractVersion: 1, ProfileID: in.Profile.ID, Exclusions: []contracts.Exclusion{{Kind: "pattern", Value: "*"}}})
	if err != nil || !out.Truncated || out.Examined != 1000 || out.Matched != 999 || len(out.Matches) != 50 || !out.MatchesTruncated {
		t.Fatal(out, err)
	}
}

// TestExcludedUnreadableDirectoryDoesNotCreateFalseGap preserves deliberate exclusion semantics.
func TestExcludedUnreadableDirectoryDoesNotCreateFalseGap(t *testing.T) {
	m, in, source := fixture(t)
	excluded := filepath.Join(source, "blocked")
	if err := os.Mkdir(excluded, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excluded, "private"), []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(excluded, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(excluded, 0700)
	in.Profile.Exclusions = []contracts.Exclusion{{Kind: "path", Value: "blocked"}}
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	run, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	result := terminal(t, m, run.Job.ID)
	if result.SnapshotState != "complete" || result.Job.EntriesVisited != "2" || result.Job.BytesObserved != "3" {
		t.Fatal(result)
	}
}

// New profiles can obtain authoritative syntax without a placeholder profile or source reads.
func TestExclusionPreviewSyntaxOnly(t *testing.T) {
	m, _, _ := fixture(t)
	defer m.Close()
	in := ExclusionPreviewRequest{ContractVersion: 1, SyntaxOnly: true, Exclusions: []contracts.Exclusion{{Kind: "pattern", Value: "**/*.tmp"}}}
	out, e := m.PreviewExclusions(context.Background(), in)
	if e != nil || !out.Valid || out.SnapshotID != nil || out.ProfileRevision != 0 || out.Examined != 0 {
		t.Fatal(out, e)
	}
	in.ProfileID = "unexpected"
	if _, e = m.PreviewExclusions(context.Background(), in); e != ErrInvalid {
		t.Fatal(e)
	}
	in.ProfileID = ""
	in.Exclusions[0].Value = "../outside"
	out, e = m.PreviewExclusions(context.Background(), in)
	if e != nil || out.Valid || out.SyntaxError == nil {
		t.Fatal(out, e)
	}
}

// Partial discovery is informational; only an explicit profile-bound job can select that generation.
func TestExclusionPreviewExplicitPartial(t *testing.T) {
	m, in, source := fixture(t)
	defer m.Close()
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(source, source+"-missing"); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(source+"-missing", source)
	started, e := m.Start(in.Profile.ID, "full")
	if e != nil {
		t.Fatal(e)
	}
	done := terminal(t, m, started.Job.ID)
	if done.SnapshotState != "partial" {
		t.Fatal(done)
	}
	request := ExclusionPreviewRequest{ContractVersion: 1, ProfileID: in.Profile.ID}
	out, e := m.PreviewExclusions(context.Background(), request)
	if e != nil || out.SnapshotID != nil || out.AvailablePartial == nil || out.AvailablePartial.JobID != done.Job.ID {
		t.Fatal(out, e)
	}
	request.PartialJobID = done.Job.ID
	out, e = m.PreviewExclusions(context.Background(), request)
	if e != nil || out.SnapshotState != "partial" || out.SnapshotID == nil || *out.SnapshotID != *done.Job.ResultSnapshotID {
		t.Fatal(out, e)
	}
	request.PartialJobID = "foreign"
	if _, e = m.PreviewExclusions(context.Background(), request); e != ErrNotFound {
		t.Fatal(e)
	}
}
