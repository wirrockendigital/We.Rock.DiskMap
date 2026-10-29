// Package diagnosticexport creates bounded diagnostic archives without runtime access.
package diagnosticexport

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version         = 1
	MaxInputBytes   = 256 * 1024
	MaxArchiveBytes = 512 * 1024
	MaxJSONDepth    = 32
	MaxFaults       = 64
	MaxFaultDepth   = 4
	MaxPrivatePaths = 128
	MaxPathBytes    = 4096
)

// Fixed errors never echo rejected private values or decoder diagnostics.
var (
	ErrInvalid = errors.New("DIAGNOSTIC_EXPORT_INVALID")
	ErrPreview = errors.New("DIAGNOSTIC_PREVIEW_MISMATCH")
	ErrArchive = errors.New("DIAGNOSTIC_ARCHIVE_FAILED")
)

// Input contains classified observations only; no free messages or identity fields exist.
type Input struct {
	Version       int           `json:"version"`
	ObservedAt    string        `json:"observedAt"`
	Versions      Versions      `json:"versions"`
	Configuration Configuration `json:"configuration"`
	Components    []Component   `json:"components"`
	Capabilities  []Capability  `json:"capabilities"`
	Faults        []Fault       `json:"faults"`
	PrivatePaths  []PrivatePath `json:"privatePaths,omitempty"`
}

// Versions accepts numeric product formats and closed architecture names, never host names.
type Versions struct {
	App          string `json:"app"`
	DSM          string `json:"dsm"`
	Architecture string `json:"architecture"`
	Control      uint64 `json:"control"`
	Generation   uint64 `json:"generation"`
	Scanner      uint64 `json:"scanner"`
}

// Configuration uses null for unknown aggregate counts rather than inventing zero.
type Configuration struct {
	Profiles          *uint64 `json:"profiles"`
	ExclusionRules    *uint64 `json:"exclusionRules"`
	RetainedSnapshots *uint64 `json:"retainedSnapshots"`
}

// Component and Capability contain only closed vocabulary observations.
type Component struct {
	Kind  string `json:"kind"`
	State string `json:"state"`
}
type Capability struct {
	Kind  string `json:"kind"`
	State string `json:"state"`
}

// Fault preserves a bounded causal classification without arbitrary messages or IDs.
type Fault struct {
	Code         string        `json:"code"`
	Count        uint64        `json:"count"`
	Children     []Fault       `json:"children,omitempty"`
	PrivatePaths []PrivatePath `json:"privatePaths,omitempty"`
}

// PrivatePath is the sole private-data channel; paths are data and are never resolved.
type PrivatePath struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// Options is an explicit caller choice, not evidence of session authorization or consent.
type Options struct {
	IncludePrivatePaths bool `json:"includePrivatePaths"`
}

// Preview exposes exactly the projected document and binds the entire input plus options.
type Preview struct {
	Digest               string
	Document             []byte
	Categories           []string
	IncludedPrivatePaths int
	OmittedPrivatePaths  int
}

var productVersion = regexp.MustCompile(`^[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}(-[0-9]{1,10})?$`)

// Decode rejects duplicates, unknown keys, trailing data and excessive nesting before projection.
func Decode(raw []byte) (Input, error) {
	var in Input
	if len(raw) == 0 || len(raw) > MaxInputBytes || !utf8.Valid(raw) {
		return in, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if strictValue(d, 0, reflect.TypeOf(in)) != nil {
		return in, ErrInvalid
	}
	if _, e := d.Token(); e != io.EOF {
		return in, ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || validate(in) != nil {
		return Input{}, ErrInvalid
	}
	return in, nil
}

// strictValue bounds recursion and refuses ambiguous duplicate members at every depth.
func strictValue(d *json.Decoder, depth int, schema reflect.Type) error {
	if depth > MaxJSONDepth {
		return ErrInvalid
	}
	t, e := d.Token()
	if e != nil {
		return ErrInvalid
	}
	if t == nil {
		if schema.Kind() == reflect.Pointer || schema.Kind() == reflect.Slice {
			return nil
		}
		return ErrInvalid
	}
	if schema.Kind() == reflect.Pointer {
		schema = schema.Elem()
	}
	delim, compound := t.(json.Delim)
	if !compound {
		if schema.Kind() == reflect.Struct || schema.Kind() == reflect.Slice {
			return ErrInvalid
		}
		return nil
	}
	switch delim {
	case '{':
		if schema.Kind() != reflect.Struct {
			return ErrInvalid
		}
		// Exact JSON tags close encoding/json's otherwise case-insensitive field aliases.
		fields := map[string]reflect.Type{}
		for i := 0; i < schema.NumField(); i++ {
			field := schema.Field(i)
			fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field.Type
		}
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			name, ok := key.(string)
			field, known := fields[name]
			if e != nil || !ok || !known || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if strictValue(d, depth+1, field) != nil {
				return ErrInvalid
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		if schema.Kind() != reflect.Slice {
			return ErrInvalid
		}
		for d.More() {
			if strictValue(d, depth+1, schema.Elem()) != nil {
				return ErrInvalid
			}
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// validate limits every text-bearing field to its technical vocabulary or isolated path channel.
func validate(in Input) error {
	at, e := time.Parse(time.RFC3339, in.ObservedAt)
	if e != nil || at.Format(time.RFC3339) != in.ObservedAt || !strings.HasSuffix(in.ObservedAt, "Z") || in.Version != Version {
		return ErrInvalid
	}
	if !productVersion.MatchString(in.Versions.App) || !productVersion.MatchString(in.Versions.DSM) || !slices.Contains([]string{"amd64", "arm64", "386", "arm", "unknown"}, in.Versions.Architecture) {
		return ErrInvalid
	}
	if in.Versions.Control == 0 || in.Versions.Generation == 0 || in.Versions.Scanner == 0 {
		return ErrInvalid
	}
	if len(in.Components) > 8 || len(in.Capabilities) > 8 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, c := range in.Components {
		if seen[c.Kind] || !slices.Contains([]string{"backend", "scanner", "queue", "control-database", "generation-index", "storage", "logger", "ui"}, c.Kind) || !slices.Contains([]string{"available", "unavailable", "unknown", "busy", "stopping", "idle", "running", "paused", "degraded"}, c.State) {
			return ErrInvalid
		}
		seen[c.Kind] = true
	}
	seen = map[string]bool{}
	for _, c := range in.Capabilities {
		if seen[c.Kind] || !slices.Contains([]string{"local-api", "snapshot-reads", "job-control", "integrity-checks", "scan", "file-station", "scheduled-scan", "system-notifications"}, c.Kind) || !slices.Contains([]string{"qualified", "unqualified", "unavailable", "unknown"}, c.State) {
			return ErrInvalid
		}
		seen[c.Kind] = true
	}
	paths, faults := 0, 0
	if validatePaths(in.PrivatePaths, &paths) != nil || validateFaults(in.Faults, 1, &faults, &paths) != nil {
		return ErrInvalid
	}
	return nil
}

// validatePaths applies one global path budget across the complete recursive document.
func validatePaths(paths []PrivatePath, count *int) error {
	*count += len(paths)
	if *count > MaxPrivatePaths {
		return ErrInvalid
	}
	for _, p := range paths {
		if !slices.Contains([]string{"source-root", "package-state", "affected-path"}, p.Kind) || len(p.Path) == 0 || len(p.Path) > MaxPathBytes || !utf8.ValidString(p.Path) || strings.ContainsAny(p.Path, "\x00\r\n") {
			return ErrInvalid
		}
	}
	return nil
}

// validateFaults accepts existing diagnostic reason codes and explicit bounded lifecycle codes.
func validateFaults(faults []Fault, depth int, count, paths *int) error {
	if len(faults) > 0 && depth > MaxFaultDepth {
		return ErrInvalid
	}
	*count += len(faults)
	if *count > MaxFaults {
		return ErrInvalid
	}
	for _, f := range faults {
		if !slices.Contains([]string{"COMPONENT_BUSY", "BACKEND_STOPPING", "CONTROL_DATABASE_UNAVAILABLE", "QUEUE_UNAVAILABLE", "INDEX_SPACE_LOW", "INDEX_SPACE_UNAVAILABLE", "SCANNER_UNAVAILABLE", "SERVICE_FAILED", "SCAN_FAILED", "SCAN_PARTIAL", "SOURCE_UNAVAILABLE", "PERMISSION_DENIED", "INDEX_CORRUPT", "INDEX_UNSUPPORTED", "FILE_STATION_UNAVAILABLE"}, f.Code) || f.Count == 0 {
			return ErrInvalid
		}
		if validatePaths(f.PrivatePaths, paths) != nil || validateFaults(f.Children, depth+1, count, paths) != nil {
			return ErrInvalid
		}
	}
	return nil
}

// Project constructs the preview from freshly decoded input and never retains caller-owned buffers.
func Project(raw []byte, options Options) (Preview, error) {
	in, e := Decode(raw)
	if e != nil {
		return Preview{}, e
	}
	// Hash the canonical complete input before stripping private fields; even omitted changes invalidate preview.
	canonical, e := json.Marshal(struct {
		Input   Input   `json:"input"`
		Options Options `json:"options"`
	}{in, options})
	if e != nil {
		return Preview{}, ErrInvalid
	}
	digest := sha256.Sum256(canonical)
	count := len(in.PrivatePaths)
	var projectFaults func([]Fault)
	projectFaults = func(fs []Fault) {
		for i := range fs {
			count += len(fs[i].PrivatePaths)
			if !options.IncludePrivatePaths {
				fs[i].PrivatePaths = nil
			}
			projectFaults(fs[i].Children)
		}
	}
	projectFaults(in.Faults)
	if !options.IncludePrivatePaths {
		in.PrivatePaths = nil
	}
	document, e := json.Marshal(in)
	if e != nil || len(document) > MaxInputBytes {
		return Preview{}, ErrInvalid
	}
	out := Preview{Digest: hex.EncodeToString(digest[:]), Document: document, Categories: []string{"versions", "configuration-counts", "components", "capabilities", "fault-codes"}}
	if options.IncludePrivatePaths {
		out.IncludedPrivatePaths = count
		if count > 0 {
			out.Categories = append(out.Categories, "private-paths")
		}
	} else {
		out.OmittedPrivatePaths = count
	}
	return out, nil
}

// Archive verifies preview binding and writes two fixed entries, without filesystem or network access.
func Archive(raw []byte, options Options, previewDigest string) ([]byte, error) {
	p, e := Project(raw, options)
	if e != nil {
		return nil, e
	}
	if previewDigest == "" || previewDigest != p.Digest {
		return nil, ErrPreview
	}
	documentHash := sha256.Sum256(p.Document)
	manifest, e := json.Marshal(struct {
		Version        int    `json:"version"`
		PrivatePaths   bool   `json:"privatePaths"`
		DocumentSHA256 string `json:"documentSha256"`
	}{Version, options.IncludePrivatePaths, hex.EncodeToString(documentHash[:])})
	if e != nil {
		return nil, ErrArchive
	}
	var buffer bytes.Buffer
	w := zip.NewWriter(&buffer)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"diagnostics.json", p.Document}, {"manifest.json", manifest}} {
		h := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		h.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		h.SetMode(0600)
		f, err := w.CreateHeader(h)
		if err != nil {
			return nil, ErrArchive
		}
		if _, err = f.Write(entry.data); err != nil {
			return nil, ErrArchive
		}
	}
	if w.Close() != nil || buffer.Len() > MaxArchiveBytes {
		return nil, ErrArchive
	}
	return buffer.Bytes(), nil
}
