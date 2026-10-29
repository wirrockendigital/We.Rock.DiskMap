package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// DiagnosticExportPreview is the exact projected document, never the unredacted input.
type DiagnosticExportPreview struct {
	Token                string          `json:"token"`
	DocumentSHA256       string          `json:"documentSha256"`
	Digest               string          `json:"digest"`
	ExpiresAt            string          `json:"expiresAt"`
	IncludePrivatePaths  bool            `json:"includePrivatePaths"`
	Document             json.RawMessage `json:"document"`
	IncludedPrivatePaths int             `json:"includedPrivatePaths"`
	OmittedPrivatePaths  int             `json:"omittedPrivatePaths"`
}

// DiagnosticExportRequest separates preview choice from explicit private download consent.
type DiagnosticExportRequest struct {
	ContractVersion     int    `json:"contractVersion"`
	IncludePrivatePaths bool   `json:"includePrivatePaths"`
	Token               string `json:"token,omitempty"`
	Digest              string `json:"digest,omitempty"`
	DocumentSHA256      string `json:"documentSha256,omitempty"`
	ConsentPrivatePaths bool   `json:"consentPrivatePaths,omitempty"`
}

type diagnosticExportEntry struct {
	principal string
	raw       []byte
	preview   diagnosticexport.Preview
	private   bool
	expires   time.Time
}
type diagnosticExportStore struct {
	mu      sync.Mutex
	entries map[string]diagnosticExportEntry
	clock   func() time.Time
}

// registerDiagnosticExport holds only bounded, expiring RAM previews; there is no persistent export cache.
func registerDiagnosticExport(mux *http.ServeMux, m *jobs.Manager) {
	registerDiagnosticExportVersion(mux, m, "/var/packages/we-rock-diskmap/INFO", "/etc/VERSION")
}

// diagnosticVersion reads fixed package metadata only, with explicit numeric validation.
func diagnosticVersion(path string, dsm bool) (string, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 16384 {
		return "", jobs.ErrUnavailable
	}
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(raw) > 16384 {
		return "", jobs.ErrUnavailable
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		if key != "version" && key != "majorversion" && key != "minorversion" && key != "productversion" && key != "buildnumber" {
			continue
		}
		if _, ok := values[key]; ok {
			return "", jobs.ErrUnavailable
		}
		// Accept one balanced shell-style quote pair, never strip arbitrary quote runs.
		value := parts[1]
		if strings.HasPrefix(value, `"`) {
			if len(value) < 2 || !strings.HasSuffix(value, `"`) {
				return "", jobs.ErrUnavailable
			}
			value = value[1 : len(value)-1]
		}
		if strings.Contains(value, `"`) {
			return "", jobs.ErrUnavailable
		}
		values[key] = value
	}
	v := values["version"]
	if dsm {
		for _, k := range []string{"majorversion", "minorversion", "buildnumber"} {
			if !regexp.MustCompile(`^[0-9]{1,10}$`).MatchString(values[k]) {
				return "", jobs.ErrUnavailable
			}
		}
		// Native DSM VERSION identifies the patch release through productversion.
		// Major/minor are independent consistency checks, not a fallback version source.
		product := values["productversion"]
		if !regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`).MatchString(product) {
			return "", jobs.ErrUnavailable
		}
		segments := strings.Split(product, ".")
		if segments[0] != values["majorversion"] || segments[1] != values["minorversion"] {
			return "", jobs.ErrUnavailable
		}
		v = product + "-" + values["buildnumber"]
	}
	if !regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(-[0-9]{1,10})?$`).MatchString(v) {
		return "", jobs.ErrUnavailable
	}
	return v, nil
}

// registerDiagnosticExportVersion supplies testable metadata locations without exposing them in requests.
func registerDiagnosticExportVersion(mux *http.ServeMux, m *jobs.Manager, info, version string) {
	s := &diagnosticExportStore{entries: map[string]diagnosticExportEntry{}, clock: time.Now}
	for _, action := range []string{"preview", "download"} {
		action := action
		mux.HandleFunc("/api/v1/diagnostic-export-"+action, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.Header().Set("Allow", "POST")
				w.WriteHeader(405)
				return
			}
			principal := r.Header.Values("X-DiskMap-Principal")
			var in DiagnosticExportRequest
			if len(principal) != 1 || principal[0] == "" || r.URL.RawQuery != "" || security.DecodeJSON(r.Body, 4096, &in) != nil || in.ContractVersion != 1 {
				viewError(w, jobs.ErrInvalid)
				return
			}
			now := s.clock()
			s.mu.Lock()
			for key, v := range s.entries {
				if !now.Before(v.expires) {
					delete(s.entries, key)
				}
			}
			if action == "download" {
				entry, ok := s.entries[in.Token]
				s.mu.Unlock()
				if !ok || entry.principal != principal[0] || in.DocumentSHA256 != diagnosticDocumentSHA(entry.preview.Document) || in.Digest != entry.preview.Digest || in.IncludePrivatePaths != entry.private || (entry.private && !in.ConsentPrivatePaths) || (!entry.private && in.ConsentPrivatePaths) {
					security.ActionError(w, 409, "DIAGNOSTIC_PREVIEW_MISMATCH")
					return
				}
				archive, e := diagnosticexport.Archive(entry.raw, diagnosticexport.Options{IncludePrivatePaths: entry.private}, in.Digest)
				if e != nil {
					viewError(w, jobs.ErrUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/zip")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Disposition", `attachment; filename="diskmap-diagnostics.zip"`)
				_, _ = w.Write(archive)
				return
			}
			count := 0
			for _, v := range s.entries {
				if v.principal == principal[0] {
					count++
				}
			}
			full := len(s.entries) >= 32 || count >= 4
			s.mu.Unlock()
			if full {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			if in.Token != "" || in.Digest != "" || in.DocumentSHA256 != "" || in.ConsentPrivatePaths {
				viewError(w, jobs.ErrInvalid)
				return
			}
			app, e := diagnosticVersion(info, false)
			if e != nil {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			dsm, e := diagnosticVersion(version, true)
			if e != nil {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			input, e := m.DiagnosticExportInput(r.Context(), principal[0], app, dsm)
			if e != nil {
				viewError(w, e)
				return
			}
			// Reuse the diagnostic endpoint's build-scoped integration qualification.
			// Other API implementations are not promoted to DSM-qualified capabilities.
			if build, err := readDSMBuild(version); err == nil {
				for i := range input.Capabilities {
					if input.Capabilities[i].Kind == "file-station" || input.Capabilities[i].Kind == "scheduled-scan" {
						input.Capabilities[i].State = "unqualified"
						if build == "90080" {
							input.Capabilities[i].State = "qualified"
						}
					}
				}
			}
			raw, e := json.Marshal(input)
			if e != nil {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			p, e := diagnosticexport.Project(raw, diagnosticexport.Options{IncludePrivatePaths: in.IncludePrivatePaths})
			if e != nil {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			tokenBytes := make([]byte, 32)
			if _, e = rand.Read(tokenBytes); e != nil {
				viewError(w, jobs.ErrUnavailable)
				return
			}
			token := hex.EncodeToString(tokenBytes)
			expires := s.clock().Add(120 * time.Second)
			s.mu.Lock()
			count = 0
			for _, v := range s.entries {
				if v.principal == principal[0] {
					count++
				}
			}
			if len(s.entries) >= 32 || count >= 4 {
				s.mu.Unlock()
				viewError(w, jobs.ErrUnavailable)
				return
			}
			s.entries[token] = diagnosticExportEntry{principal[0], raw, p, in.IncludePrivatePaths, expires}
			s.mu.Unlock()
			// Bound private-data retention even when no later request triggers lazy expiry.
			time.AfterFunc(120*time.Second, func() { s.mu.Lock(); delete(s.entries, token); s.mu.Unlock() })
			diagnosticPreviewReply(w, DiagnosticExportPreview{token, diagnosticDocumentSHA(p.Document), p.Digest, expires.UTC().Format(time.RFC3339), in.IncludePrivatePaths, p.Document, p.IncludedPrivatePaths, p.OmittedPrivatePaths})
		})
	}
}

// diagnosticPreviewReply keeps the larger allowance exclusive to this projected document.
func diagnosticPreviewReply(w http.ResponseWriter, v DiagnosticExportPreview) {
	raw, e := json.Marshal(struct {
		ContractVersion int                     `json:"contractVersion"`
		RequestID       string                  `json:"requestId"`
		Data            DiagnosticExportPreview `json:"data"`
	}{1, "local", v})
	if e != nil || len(raw) > diagnosticexport.MaxInputBytes+4096 {
		viewError(w, jobs.ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

// diagnosticDocumentSHA binds the exact public projection, without revealing the private input digest.
func diagnosticDocumentSHA(raw []byte) string {
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}
