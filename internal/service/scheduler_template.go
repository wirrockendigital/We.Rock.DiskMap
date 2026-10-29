package service

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"net/http"
)

// registerSchedulerTemplate exposes a read-only template behind existing authorization guards.
func registerSchedulerTemplate(mux *http.ServeMux, m *jobs.Manager) {
	registerSchedulerTemplateVersion(mux, m, "/etc/VERSION")
}

// registerSchedulerTemplateVersion qualifies only the measured build and actual selectable account.
func registerSchedulerTemplateVersion(mux *http.ServeMux, m *jobs.Manager, versionPath string) {
	mux.HandleFunc("GET /api/v1/profiles/{profileId}/scheduler-template", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			jobError(w, jobs.ErrInvalid)
			return
		}
		out, err := m.SchedulerTemplate(r.Context(), r.PathValue("profileId"))
		if err != nil {
			jobError(w, err)
			return
		}
		if build, e := readDSMBuild(versionPath); e == nil {
			out.DSMBuild = &build
			out.Qualification = "unqualified"
			// Root submits to the private socket; the existing daemon scans as the package user.
			if build == "90080" {
				account := "root"
				out.ExecutionAccount = &account
				out.Qualification = "package-kernel-peer"
			}
		}
		boundedReply(w, out)
	})
}
