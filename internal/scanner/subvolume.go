package scanner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// boundaryError records a conservative traversal limit without asserting snapshot ancestry.
type boundaryError struct{ code string }

func (e *boundaryError) Error() string { return e.code }

// subvolumeGuard admits only the root tree and freshly bound native registry share trees.
// A changed or unknown tree remains an explicit gap; names and readonly flags are irrelevant.
func subvolumeGuard(target string, root RootBinding, qualified []scopemanifest.QualifiedSubvolume, read func(*os.File) (string, bool, error)) func(string, *os.File) error {
	rootTree := ""
	active := map[string]string{}
	return func(relative string, file *os.File) error {
		id, btrfs, err := read(file)
		if err != nil {
			return &boundaryError{"SUBVOLUME_CLASSIFICATION_UNAVAILABLE"}
		}
		if !btrfs {
			return nil
		}
		if relative == "." {
			rootTree = id
			return nil
		}
		if rootTree != "" && id == rootTree {
			return nil
		}
		for prefix, accepted := range active {
			if id == accepted && strings.HasPrefix(relative, prefix+"/") {
				return nil
			}
		}
		for _, q := range qualified {
			if q.TargetID != target || q.RelativePath != relative || q.TreeID != id || q.Binding.Path != filepath.Join(root.Path, filepath.FromSlash(relative)) {
				continue
			}
			info, e := file.Stat()
			if e != nil {
				break
			}
			stat := info.Sys().(*syscall.Stat_t)
			mount, e := MountIdentity(file)
			if e == nil && mount == root.MountID && mount == q.Binding.MountID && strconv.FormatUint(uint64(stat.Dev), 10) == q.Binding.Device && strconv.FormatUint(uint64(stat.Ino), 10) == q.Binding.Inode {
				active[relative] = id
				return nil
			}
		}
		return &boundaryError{"SUBVOLUME_CLASSIFICATION_UNAVAILABLE"}
	}
}
