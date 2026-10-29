package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
)

// registerDiagnostics remains behind the server's peer and public session guards.
func registerDiagnostics(mux *http.ServeMux, m *jobs.Manager) {
	registerDiagnosticsVersion(mux, m, "/etc/VERSION")
}

// registerDiagnosticsVersion supports bounded tests with an actual VERSION fixture.
func registerDiagnosticsVersion(mux *http.ServeMux, m *jobs.Manager, versionPath string) {
	mux.HandleFunc("GET /api/v1/diagnostics", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			viewError(w, jobs.ErrInvalid)
			return
		}
		status, err := m.DiagnosticStatus(r.Context())
		if err != nil {
			viewError(w, jobs.ErrUnavailable)
			return
		}

		if build, e := readDSMBuild(versionPath); e == nil {
			status.Capabilities.DSMBuild = &build
			status.Capabilities.DSMFileStation = "unqualified"
			status.Capabilities.DSMScheduledScan = "unqualified"
			// Qualification is limited to the measured build and these two integration paths.
			if build == "90080" {
				status.Capabilities.DSMFileStation = "qualified-probe"
				status.Capabilities.DSMScheduledScan = "qualified-probe"
			}
		}
		boundedReply(w, status)
	})
}
