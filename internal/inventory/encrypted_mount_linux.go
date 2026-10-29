//go:build linux

package inventory

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// metadataDirectory opens each component without following links or reading directory contents.
func metadataDirectory(path string) (scanner.RootBinding, error) {
	var b scanner.RootBinding
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return b, errShareRegistry
	}
	flags := unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return b, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, e := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if e != nil {
			return b, e
		}
		fd = next
	}
	f := os.NewFile(uintptr(fd), "encrypted-backing")
	defer f.Close()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return b, err
	}
	mid, err := scanner.MountIdentity(f)
	if err != nil {
		return b, err
	}
	return scanner.RootBinding{Path: path, Device: strconv.FormatUint(uint64(st.Dev), 10), Inode: strconv.FormatUint(st.Ino, 10), MountID: mid}, nil
}

// ObserveEncryptedMounts is best-effort capability evidence; absence never authorizes rebinding.
func ObserveEncryptedMounts(ctx context.Context, volumes []contracts.Volume, shares []contracts.Share) map[string]EncryptedMount {
	out := map[string]EncryptedMount{}
	raw, err := readBoundedFile("/proc/self/mountinfo", 4<<20)
	if err != nil {
		return out
	}
	mounts, err := ParseMountInfo(raw)
	if err != nil {
		return out
	}
	unique := func(path string) (Mount, bool) {
		var found Mount
		count := 0
		for _, m := range mounts {
			if m.Path == path {
				found = m
				count++
			}
		}
		return found, count == 1
	}
	for _, s := range shares {
		if ctx.Err() != nil {
			return map[string]EncryptedMount{}
		}
		if s.Encrypted == nil || !*s.Encrypted || s.State != "reachable" {
			continue
		}
		for _, v := range volumes {
			if v.ID != s.VolumeID || !volumePath.MatchString(v.MountPath) {
				continue
			}
			vm, ok := unique(v.MountPath)
			sm, ok2 := unique(s.PhysicalPath)
			if !ok || !ok2 || vm.Filesystem != "btrfs" {
				continue
			}
			obs, e := probeNative(ctx, vm)
			if e != nil || "volume-"+obs.UUID != v.ID {
				continue
			}
			volume, e := metadataDirectory(v.MountPath)
			if e != nil || volume.MountID != vm.ID {
				continue
			}
			backing, e := metadataDirectory(filepath.Join(v.MountPath, "@"+filepath.Base(s.PhysicalPath)+"@"))
			if e != nil {
				continue
			}
			root, exposed, e := scanner.OpenBoundRoot(s.PhysicalPath)
			if e != nil {
				continue
			}
			root.Close()
			p := EncryptedMount{Version: 1, VolumeID: v.ID, Volume: volume, Backing: backing, Exposed: exposed, Mount: sm}
			sourceCount := 0
			for _, m := range mounts {
				if m.Filesystem == "ecryptfs" && m.Source == backing.Path {
					sourceCount++
				}
			}
			if sourceCount == 1 && ValidEncryptedMount(p, v, s) {
				out[s.ID] = p
			}
		}
	}
	// Reject any concurrent mount change; a later fresh catalog also rechecks descriptors.
	after, e := readBoundedFile("/proc/self/mountinfo", 4<<20)
	if e != nil || string(after) != string(raw) {
		return map[string]EncryptedMount{}
	}
	return out
}
