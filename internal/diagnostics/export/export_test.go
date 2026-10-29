package diagnosticexport

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

// fixture includes private values at every recursive level and an unknown numeric observation.
func fixture() Input {
	profiles := uint64(3)
	return Input{Version: 1, ObservedAt: "2026-10-10T22:00:00Z",
		Versions:      Versions{App: "0.1.0-1", DSM: "7.4.1-90080", Architecture: "amd64", Control: 7, Generation: 11, Scanner: 1},
		Configuration: Configuration{Profiles: &profiles},
		Components:    []Component{{Kind: "backend", State: "available"}},
		Capabilities:  []Capability{{Kind: "file-station", State: "qualified"}, {Kind: "system-notifications", State: "unqualified"}},
		PrivatePaths:  []PrivatePath{{Kind: "source-root", Path: "/volume1/Privat-Änne"}},
		Faults:        []Fault{{Code: "SCAN_PARTIAL", Count: 1, PrivatePaths: []PrivatePath{{Kind: "affected-path", Path: "/volume1/Privat-Kind"}}, Children: []Fault{{Code: "PERMISSION_DENIED", Count: 1, PrivatePaths: []PrivatePath{{Kind: "affected-path", Path: "../../Privat-Tief"}}}}}},
	}
}

// encoded provides a fresh immutable input for each assertion.
func encoded(t *testing.T, in Input) []byte {
	t.Helper()
	b, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

// unpack examines every archive entry, including metadata, rather than only the primary JSON.
func unpack(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if e != nil {
		t.Fatal(e)
	}
	if len(z.File) != 2 || z.File[0].Name != "diagnostics.json" || z.File[1].Name != "manifest.json" {
		t.Fatal("unexpected archive entries")
	}
	out := map[string][]byte{}
	for _, f := range z.File {
		if f.Method != zip.Store || f.Mode().Perm() != 0600 || f.Modified.Year() != 1980 {
			t.Fatal("unsafe archive metadata")
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			t.Fatal(e)
		}
		out[f.Name] = b
	}
	return out
}

// TestArchivePrivacy verifies recursive omission, explicit opt-in and deterministic complete ZIP bytes.
func TestArchivePrivacy(t *testing.T) {
	raw := encoded(t, fixture())
	for _, include := range []bool{false, true} {
		options := Options{IncludePrivatePaths: include}
		p, e := Project(raw, options)
		if e != nil {
			t.Fatal(e)
		}
		if include && p.IncludedPrivatePaths != 3 || !include && p.OmittedPrivatePaths != 3 {
			t.Fatal("incorrect preview scope")
		}
		a, e := Archive(raw, options, p.Digest)
		if e != nil {
			t.Fatal(e)
		}
		b, e := Archive(raw, options, p.Digest)
		if e != nil || !bytes.Equal(a, b) {
			t.Fatal("non-deterministic archive")
		}
		files := unpack(t, a)
		if !bytes.Equal(files["diagnostics.json"], p.Document) {
			t.Fatal("preview mismatch")
		}
		for _, name := range []string{"Privat-Änne", "Privat-Kind", "Privat-Tief"} {
			if bytes.Contains(a, []byte(name)) != include {
				t.Fatal("privacy projection failed")
			}
		}
		if !include && bytes.Contains(a, []byte("privatePaths\":")) && bytes.Contains(files["diagnostics.json"], []byte("privatePaths")) {
			t.Fatal("private field retained")
		}
		var document Input
		if json.Unmarshal(files["diagnostics.json"], &document) != nil || document.Configuration.ExclusionRules != nil {
			t.Fatal("unknown count changed")
		}
		var manifest struct {
			Version        int
			PrivatePaths   bool
			DocumentSHA256 string
		}
		if json.Unmarshal(files["manifest.json"], &manifest) != nil {
			t.Fatal("manifest invalid")
		}
		hash := sha256.Sum256(files["diagnostics.json"])
		if manifest.Version != 1 || manifest.PrivatePaths != include || manifest.DocumentSHA256 != hex.EncodeToString(hash[:]) {
			t.Fatal("manifest mismatch")
		}
	}
}

// TestPreviewBinding rejects cancelled previews and changes even to omitted private data.
func TestPreviewBinding(t *testing.T) {
	in := fixture()
	raw := encoded(t, in)
	p, e := Project(raw, Options{})
	if e != nil {
		t.Fatal(e)
	}
	for _, digest := range []string{"", strings.Repeat("0", 64), p.Digest[:63]} {
		if a, e := Archive(raw, Options{}, digest); a != nil || !errors.Is(e, ErrPreview) {
			t.Fatal("unbound archive")
		}
	}
	if a, e := Archive(raw, Options{IncludePrivatePaths: true}, p.Digest); a != nil || !errors.Is(e, ErrPreview) {
		t.Fatal("option not bound")
	}
	in.PrivatePaths[0].Path = "/volume1/Changed"
	if a, e := Archive(encoded(t, in), Options{}, p.Digest); a != nil || !errors.Is(e, ErrPreview) {
		t.Fatal("omitted input not bound")
	}
	// Formatting alone does not change typed input semantics.
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		t.Fatal("fixture invalid")
	}
	if _, e := Archive(pretty.Bytes(), Options{}, p.Digest); e != nil {
		t.Fatal(e)
	}
	// Mutating preview bytes cannot inject bytes into an archive rebuilt from original input.
	p.Document[0] = 'x'
	if a, e := Archive(raw, Options{}, p.Digest); e != nil || bytes.Contains(a, []byte("x\"version")) {
		t.Fatal("preview buffer injected")
	}
}

// TestStrictInput refuses private free text at every supported nesting level without echoing values.
func TestStrictInput(t *testing.T) {
	raw := string(encoded(t, fixture()))
	bad := []string{
		strings.Replace(raw, `"version":1`, `"version":1,"password":"SECRET"`, 1),
		strings.Replace(raw, `"code":"SCAN_PARTIAL"`, `"code":"SCAN_PARTIAL","rawError":"SECRET"`, 1),
		strings.Replace(raw, `"code":"PERMISSION_DENIED"`, `"code":"PERMISSION_DENIED","session":"SECRET"`, 1),
		strings.Replace(raw, `"kind":"backend"`, `"kind":"backend","message":"SECRET"`, 1),
		strings.Replace(raw, `"path":"/volume1/Privat-Kind"`, `"path":"/volume1/Privat-Kind","logmessage":"SECRET"`, 1),
		strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(raw, `"version":1`, `"version":2,"Version":1`, 1),
		strings.Replace(raw, `"version":1`, `"Version":1`, 1),
		strings.Replace(raw, `"code":"SCAN_PARTIAL"`, `"code":"PRIVATE_PERSON","Code":"SCAN_PARTIAL"`, 1),
		strings.Replace(raw, `"code":"SCAN_PARTIAL"`, `"Code":"SCAN_PARTIAL"`, 1),
		strings.Replace(raw, `"count":1`, `"count":null`, 1),
		strings.Replace(raw, `"code":"SCAN_PARTIAL"`, `"code":"SCAN_PARTIAL","code":"SCAN_FAILED"`, 1),
		strings.Replace(raw, `"version":1`, `"version":2`, 1),
		strings.Replace(raw, `"app":"0.1.0-1"`, `"app":"0.1.0-SECRET"`, 1),
		strings.Replace(raw, `"code":"SCAN_PARTIAL"`, `"code":"PRIVATE_PERSON"`, 1),
		raw + `{}`, `null`, `[]`, strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33),
		strings.Repeat(" ", MaxInputBytes) + raw,
	}
	for i, input := range bad {
		if _, e := Project([]byte(input), Options{IncludePrivatePaths: true}); !errors.Is(e, ErrInvalid) || strings.Contains(e.Error(), "SECRET") {
			t.Fatalf("case %d not safely rejected", i)
		}
	}
	invalidUTF8 := append([]byte{0xff}, []byte(raw)...)
	if _, e := Decode(invalidUTF8); !errors.Is(e, ErrInvalid) {
		t.Fatal("invalid UTF8 accepted")
	}
}

// TestBudgets checks both inclusive boundaries and global recursive accounting.
func TestBudgets(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Input)
		valid  bool
	}{
		{"max-faults", func(v *Input) {
			v.Faults = make([]Fault, MaxFaults)
			for i := range v.Faults {
				v.Faults[i] = Fault{Code: "SCAN_FAILED", Count: 1}
			}
		}, true},
		{"too-many-faults", func(v *Input) { v.Faults = make([]Fault, MaxFaults+1) }, false},
		{"depth-four", func(v *Input) { v.Faults = nestedFaults(4) }, true},
		{"depth-five", func(v *Input) { v.Faults = nestedFaults(5) }, false},
		{"max-path", func(v *Input) { v.PrivatePaths[0].Path = strings.Repeat("ä", MaxPathBytes/2) }, true},
		{"long-path", func(v *Input) { v.PrivatePaths[0].Path = strings.Repeat("ä", MaxPathBytes/2+1) }, false},
		{"max-paths", func(v *Input) {
			v.Faults = nil
			v.PrivatePaths = make([]PrivatePath, MaxPrivatePaths)
			for i := range v.PrivatePaths {
				v.PrivatePaths[i] = PrivatePath{Kind: "source-root", Path: "/bounded"}
			}
		}, true},
		{"nested-path-total", func(v *Input) {
			v.PrivatePaths = make([]PrivatePath, MaxPrivatePaths)
			for i := range v.PrivatePaths {
				v.PrivatePaths[i] = PrivatePath{Kind: "source-root", Path: "/bounded"}
			}
		}, false},
		{"zero-count", func(v *Input) { v.Faults[0].Count = 0 }, false},
		{"newline-path", func(v *Input) { v.PrivatePaths[0].Path = "/bad\nvalue" }, false},
		{"duplicate-component", func(v *Input) { v.Components = append(v.Components, v.Components[0]) }, false},
		{"unknown-capability", func(v *Input) { v.Capabilities[0].State = "supported-probably" }, false},
		{"non-utc", func(v *Input) { v.ObservedAt = "2026-10-10T22:00:00+01:00" }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := fixture()
			tc.mutate(&v)
			_, e := Project(encoded(t, v), Options{})
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, e)
			}
		})
	}
}

// nestedFaults creates a controlled causal chain for recursion-boundary tests.
func nestedFaults(depth int) []Fault {
	f := Fault{Code: "SCAN_FAILED", Count: 1}
	if depth > 1 {
		f.Children = nestedFaults(depth - 1)
	}
	return []Fault{f}
}
