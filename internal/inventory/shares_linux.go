//go:build linux

package inventory

import (
	"bytes"
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DiscoverShares uses only package-readable, build-qualified native metadata and kernel identity.
func DiscoverShares(ctx context.Context, volumes []contracts.Volume, previous []contracts.Share) (ShareResult, error) {
	version, err := readBoundedFile("/etc/VERSION", 16384)
	if err != nil {
		return ShareResult{}, errShareRegistry
	}
	buildCount := 0
	for _, line := range strings.Split(string(version), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "buildnumber=") {
			if strings.TrimSpace(line) != `buildnumber="90080"` {
				return ShareResult{}, errShareRegistry
			}
			buildCount++
		}
	}
	if buildCount != 1 {
		return ShareResult{}, errShareRegistry
	}
	smb, err := readBoundedFile("/etc/samba/smb.share.conf", 1<<20)
	if err != nil {
		return ShareResult{}, err
	}
	rightsRaw, err := readBoundedFile("/usr/syno/etc/share_right.map", 1<<20)
	if err != nil {
		return ShareResult{}, err
	}
	mappings, err := ParseSharePaths(bytes.NewReader(smb))
	if err != nil {
		return ShareResult{}, err
	}
	rights, err := ParseShareRights(bytes.NewReader(rightsRaw))
	if err != nil {
		return ShareResult{}, err
	}
	if len(rights) > 1024 {
		return ShareResult{}, errShareRegistry
	}
	// External USB policy records are not internal-volume registrations.
	mappings, rights, err = internalShareRegistry(mappings, rights)
	if err != nil {
		return ShareResult{}, err
	}
	before, err := readBoundedFile("/proc/self/mountinfo", 4<<20)
	if err != nil {
		return ShareResult{}, err
	}
	mounts, err := ParseMountInfo(before)
	if err != nil {
		return ShareResult{}, err
	}
	qualified := map[string]Mount{}
	for _, v := range volumes {
		if v.State != "reachable" {
			continue
		}
		var matched []Mount
		for _, m := range mounts {
			if m.Path == v.MountPath {
				matched = append(matched, m)
			}
		}
		if len(matched) != 1 {
			return ShareResult{}, errShareRegistry
		}
		observed, e := probeNative(ctx, matched[0])
		if e != nil || "volume-"+observed.UUID != v.ID {
			return ShareResult{}, errShareRegistry
		}
		qualified[v.MountPath] = matched[0]
	}
	for name := range mappings {
		if _, ok := rights[name]; !ok {
			return ShareResult{}, errShareRegistry
		}
	}
	records := []RegisteredShare{}
	for name, right := range rights {
		physical := right.Path
		if err = ctx.Err(); err != nil {
			return ShareResult{}, err
		}
		smbPath, smbPresent := mappings[name]
		if (right.Locked && smbPresent) || (!right.Locked && (!smbPresent || smbPath != physical)) {
			return ShareResult{}, errShareRegistry
		}
		// DSM's verified native layout registers shares directly beneath their real volume root.
		volumeMount := filepath.Dir(physical)
		volume, ok := qualified[volumeMount]
		if !ok || physical != filepath.Join(volumeMount, name) {
			return ShareResult{}, errShareRegistry
		}
		uuid, e := ReadShareUUID(volumeMount, name)
		if e != nil {
			return ShareResult{}, e
		}
		encryption := 0
		if right.Encrypted {
			encryption = 1
			found := 0
			for _, m := range mounts {
				if m.Path == physical {
					found++
					if m.Filesystem != "ecryptfs" || m.ParentID != volume.ID || m.Root != "/" || m.Source != filepath.Join(volumeMount, "@"+name+"@") {
						return ShareResult{}, errShareRegistry
					}
					encryption = 2
				}
			}
			if (right.Locked && found != 0) || (!right.Locked && found != 1) {
				return ShareResult{}, errShareRegistry
			}
		}
		records = append(records, RegisteredShare{UUID: uuid, Name: name, VolumeMountPath: volumeMount, PhysicalPath: physical, FileStationPath: "/" + name, Encryption: &encryption, IdentityEvidence: "DSM90080:SYNO@.attr:" + uuid})
	}
	probe := func(ctx context.Context, r RegisteredShare) (ShareObservation, error) {
		if err := ctx.Err(); err != nil {
			return ShareObservation{}, err
		}
		volume := qualified[r.VolumeMountPath]
		expectedMount := volume.ID
		if r.Encryption != nil && *r.Encryption == 2 {
			for _, m := range mounts {
				if m.Path == r.PhysicalPath {
					expectedMount = m.ID
				}
			}
		}
		id := ""
		for _, v := range volumes {
			if v.MountPath == r.VolumeMountPath {
				id = v.ID
			}
		}
		root, _, e := scanner.OpenBoundRoot(r.PhysicalPath)
		if os.IsNotExist(e) {
			return ShareObservation{VolumeID: id, Exists: false}, nil
		}
		if os.IsPermission(e) {
			return ShareObservation{VolumeID: id, Exists: true, Readable: false}, nil
		}
		if e != nil {
			return ShareObservation{}, e
		}
		defer root.Close()
		file, e := root.Open(".")
		if e != nil {
			return ShareObservation{VolumeID: id, Exists: true, Readable: false}, nil
		}
		defer file.Close()
		fdinfo, e := readBoundedFile("/proc/self/fdinfo/"+strconv.FormatUint(uint64(file.Fd()), 10), 16384)
		if e != nil {
			return ShareObservation{}, e
		}
		actual := ""
		for _, line := range strings.Split(string(fdinfo), "\n") {
			f := strings.Fields(line)
			if len(f) == 2 && f[0] == "mnt_id:" {
				actual = f[1]
			}
		}
		if actual != expectedMount {
			return ShareObservation{}, errShareRegistry
		}
		_, e = file.Readdirnames(1)
		if e != nil && e != io.EOF {
			return ShareObservation{VolumeID: id, Exists: true, Readable: false}, nil
		}
		return ShareObservation{VolumeID: id, Exists: true, Readable: true}, nil
	}
	result, err := CollectShares(ctx, records, volumes, nil, probe)
	if err != nil {
		return result, err
	}
	// Every metadata source is reread; publication requires one unchanged observation window.
	for _, source := range []struct {
		path   string
		before []byte
		limit  int
	}{{"/etc/samba/smb.share.conf", smb, 1 << 20}, {"/usr/syno/etc/share_right.map", rightsRaw, 1 << 20}, {"/proc/self/mountinfo", before, 4 << 20}} {
		after, e := readBoundedFile(source.path, source.limit)
		if e != nil || !bytes.Equal(source.before, after) {
			return ShareResult{}, errShareRegistry
		}
	}
	for _, r := range records {
		uuid, e := ReadShareUUID(r.VolumeMountPath, r.Name)
		if e != nil || uuid != r.UUID {
			return ShareResult{}, errShareRegistry
		}
	}
	// Native package access is authoritative; SMB/FTP policy flags do not govern local reads.
	result.Shares = readableShares(result.Shares)
	result.Issues = []Issue{}
	return result, nil
}

// readBoundedFile never returns unbounded configuration or kernel data.
func readBoundedFile(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errShareRegistry
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, errShareRegistry
	}
	return raw, nil
}
