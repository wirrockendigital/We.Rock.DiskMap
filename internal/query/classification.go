package query

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"sort"
)

// Classification separates file-type inference from object kind and descriptive path context.
// Folder categories describe the original sealed subtree, never a claimed type of the folder itself.
type Classification struct {
	Category          *string  `json:"category"`
	Basis             string   `json:"basis"`
	MappingRevision   uint64   `json:"mappingRevision"`
	FolderComposition string   `json:"folderComposition"`
	FolderCategories  []string `json:"folderCategories"`
	ContextHints      []string `json:"contextHints"`
}

// classifyRecorded reads no content and accepts no external MIME or last-use inference.
// Metadata kind wins: a directory or special named picture.jpg is not an image file.
func classifyRecorded(n contracts.Node, name, rawPath []byte, aggregate sql.NullString, m contracts.FileTypeMapping) (Classification, error) {
	out := Classification{Basis: "not-applicable", MappingRevision: m.Revision, FolderComposition: "not-directory", FolderCategories: []string{}, ContextHints: []string{}}
	if n.Kind == "file" {
		c := category(name, m)
		out.Category = &c
		out.Basis = "unknown"
		suffix, ok := index.RecordedExtension(name)
		if ok {
			for _, a := range m.Assignments {
				if a.Extension == suffix {
					out.Basis = "extension"
					break
				}
			}
		}
	}
	if n.Kind == "directory" {
		if !aggregate.Valid || len(aggregate.String) > 32768 {
			return out, ErrSnapshot
		}
		var a contracts.DirectoryAggregate
		if json.Unmarshal([]byte(aggregate.String), &a) != nil {
			return out, ErrSnapshot
		}
		for key, v := range a.Subtree.Types {
			if !categories[key] || len(v.FileCount) > 128 || v.FileCount.Validate() != nil {
				return out, ErrSnapshot
			}
			if v.FileCount != "0" {
				out.FolderCategories = append(out.FolderCategories, key)
			}
		}
		sort.Strings(out.FolderCategories)
		out.FolderComposition = "no-regular-files"
		if len(out.FolderCategories) == 1 {
			out.FolderComposition = "single-category"
		} else if len(out.FolderCategories) > 1 {
			out.FolderComposition = "mixed"
		}
	}
	// Only ancestor components, or the directory itself, may provide this name hint.
	parts := bytes.Split(rawPath, []byte{'/'})
	if n.Kind != "directory" && len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	for _, part := range parts {
		if bytes.Equal(part, []byte("docker")) {
			out.ContextHints = append(out.ContextHints, "docker-directory-name")
			break
		}
	}
	return out, nil
}
