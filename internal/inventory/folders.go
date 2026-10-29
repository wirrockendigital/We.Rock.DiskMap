package inventory

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"path"
	"sort"
	"time"
)

// FolderRequest selects a current share, never a physical or File Station path.
type FolderRequest struct {
	ContractVersion int     `json:"contractVersion"`
	ShareID         string  `json:"shareId"`
	RelativePath    string  `json:"relativePath"`
	After           *string `json:"after"`
}

// FolderEntry is a selectable directory label and its share-relative identity.
type FolderEntry struct {
	Name         string `json:"name"`
	RelativePath string `json:"relativePath"`
}

// ScopeFolders is a bounded directory-only observation, not a scan or durable source authorization.
type ScopeFolders struct {
	ShareID      string        `json:"shareId"`
	RelativePath string        `json:"relativePath"`
	Entries      []FolderEntry `json:"entries"`
	NextCursor   *string       `json:"nextCursor"`
	CheckedAt    string        `json:"checkedAt"`
}

// Validate rejects malformed paths before inventory or filesystem access.
func (r FolderRequest) Validate() error {
	if r.ContractVersion != 1 || !security.ValidID(r.ShareID) || !contracts.ValidFolderPath(r.RelativePath, true) || r.After != nil && (!contracts.ValidFolderPath(*r.After, false) || path.Base(*r.After) != *r.After) {
		return security.ErrInvalidRequest
	}
	return nil
}

// ValidFolders checks sorted unique children and the bounded response at the CGI boundary.
func ValidFolders(v ScopeFolders) bool {
	if (FolderRequest{ContractVersion: 1, ShareID: v.ShareID, RelativePath: v.RelativePath}).Validate() != nil || v.Entries == nil || len(v.Entries) > 128 {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, v.CheckedAt); err != nil {
		return false
	}
	last := ""
	for _, entry := range v.Entries {
		if entry.Name <= last || path.Base(entry.Name) != entry.Name || !contracts.ValidFolderPath(entry.Name, false) || entry.RelativePath != path.Join(v.RelativePath, entry.Name) || !contracts.ValidFolderPath(entry.RelativePath, false) {
			return false
		}
		last = entry.Name
	}
	return v.NextCursor == nil || len(v.Entries) == 128 && *v.NextCursor == last
}

// BrowseFolders performs a bounded uncached observation for callers without a browser lifetime.
func BrowseFolders(ctx context.Context, r FolderRequest, volumes []contracts.Volume, shares []contracts.Share) (ScopeFolders, error) {
	return browseFolders(ctx, r, volumes, shares, nil)
}

// browseFolders bounds enumeration separately from its 128-row public cursor page.
func browseFolders(ctx context.Context, r FolderRequest, volumes []contracts.Volume, shares []contracts.Share, browser *FolderBrowser) (ScopeFolders, error) {
	out := ScopeFolders{ShareID: r.ShareID, RelativePath: r.RelativePath, Entries: []FolderEntry{}}
	if err := r.Validate(); err != nil {
		return out, err
	}
	scope := contracts.Scope{Kind: "shares", TargetIDs: []string{r.ShareID}}
	if r.RelativePath != "." {
		scope.Kind = "folder"
		scope.RelativePath = r.RelativePath
	}
	n, err := Normalize(scope, volumes, shares)
	if err != nil || len(n.Roots) != 1 || n.Roots[0].State != "reachable" {
		return out, ErrRootChanged
	}

	root, binding, err := OpenScopeRoot(n.Roots[0])
	if err != nil {
		return out, err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return out, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return out, err
	}
	names, cached := browser.get(binding, before, r)
	if !cached {
		names = []FolderEntry{}
		count, retained := 0, 0
		for {
			if err = ctx.Err(); err != nil {
				return out, err
			}
			rows, e := file.ReadDir(128)
			count += len(rows)
			// Deadline and independent byte/count budgets bound even file-heavy directories.
			if count > 1000000 {
				return out, security.ErrInvalidRequest
			}
			for _, entry := range rows {
				name := entry.Name()
				if !contracts.ValidFolderPath(name, false) || path.Base(name) != name {
					continue
				}
				// os.ReadDir resolves unknown d_type internally; IsDir needs no extra stat.
				if !entry.IsDir() {
					continue
				}

				rel := path.Join(r.RelativePath, name)
				if !contracts.ValidFolderPath(rel, false) {
					continue
				}
				retained += len(name) + len(rel) + 32
				if len(names) >= 32768 || retained > 2<<20 {
					return out, security.ErrInvalidRequest
				}
				names = append(names, FolderEntry{name, rel})
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return out, e
			}
		}
		sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	}
	after, err := file.Stat()
	if err != nil || !before.ModTime().Equal(after.ModTime()) {
		return out, ErrRootChanged
	}
	// Re-open the exact mapping after enumeration; renamed or replaced roots invalidate the observation.
	check, current, err := OpenScopeRoot(n.Roots[0])
	if err != nil {
		return out, err
	}
	check.Close()
	if current != binding {
		return out, ErrRootChanged
	}
	// Cache only a fully requalified immutable listing, never a failed prefix.
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !cached {
		browser.put(binding, after, names, r)
	}
	start := 0
	if r.After != nil {
		start = sort.Search(len(names), func(i int) bool { return names[i].Name > *r.After })
	}
	end := start + 128
	if end > len(names) {
		end = len(names)
	}
	out.Entries = append(out.Entries, names[start:end]...)
	if end < len(names) {
		last := out.Entries[len(out.Entries)-1].Name
		out.NextCursor = &last
	}

	out.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	return out, nil
}
