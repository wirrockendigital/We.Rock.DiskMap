package inventory

import (
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"os"
	"path/filepath"
	"strings"
)

// OpenScopeRoot binds a selected subtree to a held share and refuses symlinks and mount escapes.
func OpenScopeRoot(r NormalizedRoot) (*os.Root, scanner.RootBinding, error) {
	if r.RelativePath == "" {
		return scanner.OpenBoundRoot(r.PhysicalPath)
	}
	var empty scanner.RootBinding
	if r.Kind != "share" || !contracts.ValidFolderPath(r.RelativePath, false) {
		return nil, empty, ErrRootChanged
	}
	suffix := "/" + r.RelativePath
	if !strings.HasSuffix(r.PhysicalPath, suffix) {
		return nil, empty, ErrRootChanged
	}
	sharePath := strings.TrimSuffix(r.PhysicalPath, suffix)
	if filepath.Join(sharePath, r.RelativePath) != r.PhysicalPath {
		return nil, empty, ErrRootChanged
	}
	share, anchor, err := scanner.OpenBoundRoot(sharePath)
	if err != nil {
		return nil, empty, err
	}
	defer share.Close()
	info, err := scanner.InspectBoundPath(anchor, r.RelativePath)
	if err != nil {
		return nil, empty, err
	}
	root, binding, err := scanner.OpenBoundRoot(r.PhysicalPath)
	if err != nil {
		return nil, empty, err
	}
	observed, err := root.Stat(".")
	if err != nil || !info.IsDir() || !os.SameFile(info, observed) || binding.Device != anchor.Device || binding.MountID != anchor.MountID {
		root.Close()
		return nil, empty, ErrRootChanged
	}
	return root, binding, nil
}
