package jobs

import (
	"context"
	"encoding/json"
	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"strings"
	"testing"
)

// Real configuration labels remain private even when embedded in profile exclusions.
func TestDiagnosticExportProjection(t *testing.T) {
	m, in, root := fixture(t)
	defer m.Close()
	in.Profile.Name = "private-owner-name"
	if e := m.ImportProfile(in); e != nil {
		t.Fatal(e)
	}
	input, e := m.DiagnosticExportInput(context.Background(), "private-admin", "0.1.0-1", "7.4.1-90080")
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(input)
	normal, e := diagnosticexport.Project(raw, diagnosticexport.Options{})
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range []string{root, "private-owner-name", "private-admin", m.state} {
		if strings.Contains(string(normal.Document), s) {
			t.Fatal("private data in default projection")
		}
	}
	private, e := diagnosticexport.Project(raw, diagnosticexport.Options{IncludePrivatePaths: true})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(private.Document), root) || !strings.Contains(string(private.Document), m.state) || normal.Digest == private.Digest {
		t.Fatal("private option not explicitly bound")
	}
	if input.Configuration.Profiles == nil || *input.Configuration.Profiles != 1 || input.Configuration.RetainedSnapshots != nil {
		t.Fatal("invented configuration observation")
	}
	if _, e = m.DiagnosticExportInput(context.Background(), "", "0.1.0-1", "7.4.1-90080"); e == nil {
		t.Fatal("missing principal accepted")
	}
}
