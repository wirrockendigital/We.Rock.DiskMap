package index

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// AggregateVersion names the exact direct/subtree and unknown-regular counting contract.
const AggregateVersion = 1

// Categories are bounded independently of the configurable extension table.
var aggregateCategories = map[string]bool{"image": true, "video": true, "audio": true, "document": true, "archive": true, "diskImage": true, "database": true, "otherUnknown": true}

// asciiFold preserves Unicode bytes and avoids implicit normalization or locale dependence.
func asciiFold(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// CanonicalFileTypeMapping validates and copies a bounded configuration deterministically.
func CanonicalFileTypeMapping(m contracts.FileTypeMapping) (contracts.FileTypeMapping, error) {
	if m.Version != 1 || m.Revision == 0 || m.Revision > 9007199254740991 || len(m.Assignments) > 256 {
		return m, ErrGeneration
	}
	out := m
	out.Assignments = make([]contracts.ExtensionMapping, 0, len(m.Assignments))
	seen := map[string]bool{}
	for _, a := range m.Assignments {
		a.Extension = asciiFold(a.Extension)
		if len(a.Extension) == 0 || len(a.Extension) > 64 || !utf8.ValidString(a.Extension) || !aggregateCategories[a.Category] || seen[a.Extension] {
			return m, ErrGeneration
		}
		for _, r := range a.Extension {
			if r == '.' || r == '/' || r == '\\' || unicode.IsControl(r) || unicode.IsSpace(r) {
				return m, ErrGeneration
			}
		}
		seen[a.Extension] = true
		out.Assignments = append(out.Assignments, a)
	}
	sort.Slice(out.Assignments, func(i, j int) bool { return out.Assignments[i].Extension < out.Assignments[j].Extension })
	// JSON escaping may expand valid suffix bytes; persistence and readers share this exact budget.
	encoded, err := json.Marshal(out)
	if err != nil || len(encoded) > 32768 {
		return m, ErrGeneration
	}
	return out, nil
}

// DefaultFileTypeMapping returns independent initial settings, not a fallback for invalid state.
func DefaultFileTypeMapping() contracts.FileTypeMapping {
	m := contracts.FileTypeMapping{Version: 1, Revision: 1}
	groups := map[string]string{"image": "jpg jpeg png gif webp heic tiff svg", "video": "mp4 mkv mov avi webm", "audio": "mp3 flac wav aac ogg", "document": "txt pdf doc docx odt rtf xls xlsx csv ppt pptx", "archive": "zip gz tar bz2 xz 7z rar", "diskImage": "iso img vhd vhdx vmdk", "database": "db sqlite sqlite3 mdb accdb"}
	for category, list := range groups {
		for _, extension := range strings.Fields(list) {
			m.Assignments = append(m.Assignments, contracts.ExtensionMapping{Extension: extension, Category: category})
		}
	}
	m, _ = CanonicalFileTypeMapping(m)
	return m
}

// RecordedExtension extracts the single final suffix from recorded raw bytes without content access.
// ASCII case folding is the frozen mapping contract; Unicode suffixes remain exact.
func RecordedExtension(raw []byte) (string, bool) {
	if !utf8.Valid(raw) {
		return "", false
	}
	p := bytes.LastIndexByte(raw, '.')
	if p <= 0 || p == len(raw)-1 {
		return "", false
	}
	return asciiFold(string(raw[p+1:])), true
}

// classifyName shares raw suffix semantics with query classification and filters.
func classifyName(raw []byte, m map[string]string) string {
	suffix, ok := RecordedExtension(raw)
	if !ok {
		return "otherUnknown"
	}
	if v := m[suffix]; v != "" {
		return v
	}
	return "otherUnknown"
}
