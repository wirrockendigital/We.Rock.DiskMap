package inventory

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// FolderBrowser retains only bounded, short-lived directory names, never authority or open descriptors.
// Every page still opens the current qualified source and checks its identity and metadata.
type FolderBrowser struct {
	mu      sync.Mutex
	entries map[folderCacheKey]folderListing
}

// Alias shares and nested scopes may bind the same directory with different public paths.
type folderCacheKey struct {
	binding     scanner.RootBinding
	share, path string
}

type folderListing struct {
	names    []FolderEntry
	modified time.Time
	mode     os.FileMode
	expires  time.Time
}

// Browse reuses one enumeration across cursor pages; callers own current catalogue authorization.
func (b *FolderBrowser) Browse(ctx context.Context, r FolderRequest, volumes []contracts.Volume, shares []contracts.Share) (ScopeFolders, error) {
	return browseFolders(ctx, r, volumes, shares, b)
}

// get returns a private immutable name slice only while the exact directory remains unchanged.
func (b *FolderBrowser) get(binding scanner.RootBinding, info os.FileInfo, r FolderRequest) ([]FolderEntry, bool) {
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.entries[folderCacheKey{binding, r.ShareID, r.RelativePath}]
	if !ok {
		return nil, false
	}
	if time.Now().After(v.expires) || !v.modified.Equal(info.ModTime()) || v.mode != info.Mode() {
		delete(b.entries, folderCacheKey{binding, r.ShareID, r.RelativePath})
		return nil, false
	}
	return v.names, true
}

// put bounds both retained directory count and UTF-8 name/path bytes; eviction never changes scan data.
func (b *FolderBrowser) put(binding scanner.RootBinding, info os.FileInfo, names []FolderEntry, r FolderRequest) {
	if b == nil {
		return
	}
	bytes := 0
	for _, n := range names {
		bytes += len(n.Name) + len(n.RelativePath) + 32
	}
	if bytes > 2<<20 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.entries == nil {
		b.entries = map[folderCacheKey]folderListing{}
	}
	if len(b.entries) >= 8 {
		var key folderCacheKey
		var expires time.Time
		for k, v := range b.entries {
			if expires.IsZero() || v.expires.Before(expires) {
				key, expires = k, v.expires
			}
		}
		delete(b.entries, key)
	}
	b.entries[folderCacheKey{binding, r.ShareID, r.RelativePath}] = folderListing{names: names, modified: info.ModTime(), mode: info.Mode(), expires: time.Now().Add(15 * time.Second)}
}
