package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"regexp"
	"time"
)

// ValidDiagnosticPreview checks privacy projection and option binding before the final session guard.
func ValidDiagnosticPreview(raw []byte, in DiagnosticExportRequest) bool {
	var v contracts.Envelope[DiagnosticExportPreview]
	if security.DecodeJSON(bytes.NewReader(raw), diagnosticexport.MaxInputBytes+4096, &v) != nil || v.ContractVersion != 1 {
		return false
	}
	p := v.Data
	hex64 := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if !hex64.MatchString(p.Token) || !hex64.MatchString(p.Digest) || p.DocumentSHA256 != diagnosticDocumentSHA(p.Document) || p.IncludePrivatePaths != in.IncludePrivatePaths || p.IncludedPrivatePaths < 0 || p.OmittedPrivatePaths < 0 || p.IncludedPrivatePaths+p.OmittedPrivatePaths > diagnosticexport.MaxPrivatePaths {
		return false
	}
	if _, e := time.Parse(time.RFC3339, p.ExpiresAt); e != nil {
		return false
	}
	projected, e := diagnosticexport.Project(p.Document, diagnosticexport.Options{IncludePrivatePaths: p.IncludePrivatePaths})
	if e != nil {
		return false
	}
	// A standard preview may not conceal private fields inside a valid private input.
	if !p.IncludePrivatePaths && (projected.OmittedPrivatePaths != 0 || p.IncludedPrivatePaths != 0) {
		return false
	}
	return projected.IncludedPrivatePaths == p.IncludedPrivatePaths
}

// ValidDiagnosticArchive inspects both fixed entries without extracting any file.
func ValidDiagnosticArchive(raw []byte, private bool, expectedSHA string) bool {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil || len(z.File) != 2 {
		return false
	}
	entries := map[string][]byte{}
	for _, f := range z.File {
		if (f.Name != "diagnostics.json" && f.Name != "manifest.json") || f.Method != zip.Store || f.UncompressedSize64 > diagnosticexport.MaxInputBytes || entries[f.Name] != nil {
			return false
		}
		reader, e := f.Open()
		if e != nil {
			return false
		}
		data, e := io.ReadAll(io.LimitReader(reader, diagnosticexport.MaxInputBytes+1))
		reader.Close()
		if e != nil || len(data) > diagnosticexport.MaxInputBytes {
			return false
		}
		entries[f.Name] = data
	}
	var manifest struct {
		Version        int    `json:"version"`
		PrivatePaths   bool   `json:"privatePaths"`
		DocumentSHA256 string `json:"documentSha256"`
	}
	if security.DecodeJSON(bytes.NewReader(entries["manifest.json"]), 4096, &manifest) != nil || manifest.Version != 1 || manifest.PrivatePaths != private {
		return false
	}
	digest := sha256.Sum256(entries["diagnostics.json"])
	if hex.EncodeToString(digest[:]) != manifest.DocumentSHA256 || manifest.DocumentSHA256 != expectedSHA {
		return false
	}
	p, e := diagnosticexport.Project(entries["diagnostics.json"], diagnosticexport.Options{IncludePrivatePaths: private})
	return e == nil && (private || p.OmittedPrivatePaths == 0)
}
