package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
)

// Templates select the privileged task caller without elevating the package scanner.
func TestSchedulerTemplateQualification(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, "source")
	os.Mkdir(source, 0700)
	m, e := jobs.Open(context.Background(), filepath.Join(base, "state"))
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	p := contracts.Profile{ID: "scheduler", Revision: 1, Name: "Quotes ' \" Grüße", Active: true, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{"share"}}, ResourceMode: "gentle", Limits: contracts.ResourceLimits{Workers: 1}}
	if e = m.ImportProfile(jobs.Import{SourceMode: "isolated_roots", Profile: p, Roots: map[string]string{"share": source}, VolumeIDs: map[string]string{"share": "volume"}, RootKinds: map[string]string{"share": "share"}}); e != nil {
		t.Fatal(e)
	}
	version := filepath.Join(base, "VERSION")
	mux := http.NewServeMux()
	registerSchedulerTemplateVersion(mux, m, version)
	for _, build := range []string{"90080", "99999", "invalid"} {
		os.WriteFile(version, []byte("buildnumber=\""+build+"\"\n"), 0600)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/profiles/scheduler/scheduler-template", nil))
		var out contracts.Envelope[jobs.SchedulerTemplate]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if build == "90080" {
			if out.Data.ExecutionAccount == nil || *out.Data.ExecutionAccount != "root" || out.Data.Qualification != "package-kernel-peer" {
				t.Fatal(out)
			}
		} else if out.Data.ExecutionAccount != nil || out.Data.Qualification == "package-kernel-peer" {
			t.Fatal(out)
		}
	}
}
