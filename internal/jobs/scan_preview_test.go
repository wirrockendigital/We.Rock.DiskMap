package jobs

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"strings"
	"testing"
)

// RAM previews belong only to the browser that created a currently active matching revision.
func TestScanPreviewOwnershipAndDisposal(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	entered, release := phaseBarrier(t, m, "validating")
	started, err := m.StartBrowser(in.Profile.ID, "full", BrowserStartIdentity{Principal: "admin", Key: "preview-start"})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	owner := started.Outcome
	owner.PreviewPrincipal = "admin"
	sample := scanner.Preview{Sequence: 100, CapturedAt: "2026-10-14T12:00:00Z", Entries: "2", KnownLogicalBytes: "3", Segments: []scanner.PreviewSegment{{ID: "preview-only", Name: "data", Path: "data", Kind: "file", Bytes: "3"}}}
	m.capturePreview(owner, sample)
	sample.Segments[0].Name = "mutated"
	for _, principal := range []string{"admin", "other"} {
		result, e := m.ReadScanPreview(context.Background(), principal, started.Job.ID, in.Profile.ID)
		if e != nil {
			t.Fatal(e)
		}
		if principal == "admin" {
			if result.Preview == nil || result.Preview.Segments[0].Name != "data" {
				t.Fatal(result)
			}
			result.Preview.Segments[0].Name = "caller-mutated"
		} else if result.Preview != nil {
			t.Fatal("cross-principal preview")
		}
	}
	invalid := sample
	invalid.Sequence = 101
	invalid.Segments = make([]scanner.PreviewSegment, 65)
	m.capturePreview(owner, invalid)
	// Field lengths alone do not bound JSON escaping; reject an oversized encoded payload.
	oversized := sample
	oversized.Sequence = 102
	oversized.Segments = make([]scanner.PreviewSegment, 64)
	for i := range oversized.Segments {
		oversized.Segments[i] = scanner.PreviewSegment{ID: "preview", Name: strings.Repeat("\x01", 256), Path: strings.Repeat("\x01", 768), Kind: "file", Bytes: "3"}
	}
	encoded, _ := json.Marshal(oversized)
	if len(encoded) <= 120*1024 {
		t.Fatal("fixture does not exceed encoded budget")
	}
	m.capturePreview(owner, oversized)
	result, e := m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
	if e != nil || result.Preview == nil || result.Preview.Sequence != 100 || result.Preview.Segments[0].Name != "data" {
		t.Fatal(result, e)
	}
	raw, _ := json.Marshal(owner)
	if strings.Contains(string(raw), "PreviewPrincipal") || strings.Contains(string(raw), "admin") {
		t.Fatal("owner serialized")
	}
	release()
	terminal(t, m, started.Job.ID)
	result, e = m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
	if e != nil || result.Preview != nil {
		t.Fatal("terminal preview", result, e)
	}
}

// CLI scans never acquire a browser owner even when an administrator polls their job.
func TestScanPreviewCLIUnavailable(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	entered, release := phaseBarrier(t, m, "validating")
	started, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	result, err := m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
	if err != nil || result.Preview != nil {
		t.Fatal(result, err)
	}
	release()
	terminal(t, m, started.Job.ID)
}

// Revision changes and cancellation revoke a captured preview before any later callback can expose it.
func TestScanPreviewRevocation(t *testing.T) {
	for _, mode := range []string{"cancel", "revision"} {
		t.Run(mode, func(t *testing.T) {
			m, in, _ := fixture(t)
			if err := m.ImportProfile(in); err != nil {
				t.Fatal(err)
			}
			entered, release := phaseBarrier(t, m, "validating")
			started, err := m.StartBrowser(in.Profile.ID, "full", BrowserStartIdentity{Principal: "admin", Key: "preview-revoke"})
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			owner := started.Outcome
			owner.PreviewPrincipal = "admin"
			sample := scanner.Preview{Sequence: 100, Entries: "2", KnownLogicalBytes: "3", Segments: []scanner.PreviewSegment{}}
			m.capturePreview(owner, sample)
			if mode == "cancel" {
				m.mu.Lock()
				m.runner.cancel()
				m.mu.Unlock()
			} else {
				if _, err = m.db.Exec(`UPDATE profiles SET profile_json=json_set(profile_json,'$.revision',2) WHERE id=?`, in.Profile.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
			if err != nil || result.Preview != nil {
				t.Fatal(mode, result, err)
			}
			m.capturePreview(owner, sample)
			result, err = m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
			if err != nil || result.Preview != nil {
				t.Fatal("late observer revived preview", result, err)
			}
			release()
			terminal(t, m, started.Job.ID)
			m.capturePreview(owner, sample)
			result, err = m.ReadScanPreview(context.Background(), "admin", started.Job.ID, in.Profile.ID)
			if err != nil || result.Preview != nil {
				t.Fatal("terminal observer revived preview", result, err)
			}
		})
	}
}
