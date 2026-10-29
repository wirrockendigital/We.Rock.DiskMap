// Package inventory identifies verified DSM data filesystems independently of device names.
package inventory

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Mount is a decoded Linux mount record; device numbers are not persistent volume IDs.
type Mount struct {
	ID         string
	ParentID   string
	Device     string
	Root       string
	Path       string
	Filesystem string
	Source     string
}

// Observation comes from a held mount descriptor, including its persistent Btrfs FSID.
type Observation struct {
	UUID                               string
	MountID                            string
	BlockSize, Blocks, Free, Available uint64
}

// Issue describes omitted or uncertain inventory without exposing arbitrary kernel errors.
type Issue struct {
	Code      string `json:"code"`
	MountPath string `json:"mountPath"`
}

// Result preserves known missing volumes and reports unverified candidates separately.
type Result struct {
	Volumes []contracts.Volume `json:"volumes"`
	Issues  []Issue            `json:"issues"`
}

// Probe is the narrow boundary between kernel observation and pure inventory normalization.
type Probe func(context.Context, Mount) (Observation, error)

var volumePath = regexp.MustCompile(`^/volume[1-9][0-9]*$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParseMountInfo handles Linux escape sequences and optional mount propagation fields.
func ParseMountInfo(data []byte) ([]Mount, error) {
	mounts := []Mount{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := -1
		for i, f := range fields {
			if f == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			return nil, errors.New("INVALID_MOUNTINFO")
		}
		if _, err := strconv.ParseUint(fields[0], 10, 64); err != nil {
			return nil, errors.New("INVALID_MOUNTINFO")
		}
		root, err := unescape(fields[3])
		if err != nil {
			return nil, err
		}
		mount, err := unescape(fields[4])
		if err != nil {
			return nil, err
		}
		source, err := unescape(fields[separator+2])
		if err != nil {
			return nil, err
		}
		mounts = append(mounts, Mount{ID: fields[0], ParentID: fields[1], Device: fields[2], Root: root, Path: mount, Filesystem: fields[separator+1], Source: source})
	}
	if scanner.Err() != nil {
		return nil, errors.New("INVALID_MOUNTINFO")
	}
	return mounts, nil
}

// unescape accepts only the documented mountinfo pathname escapes.
func unescape(value string) (string, error) {
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("INVALID_MOUNTINFO")
		}
		switch value[i+1 : i+4] {
		case "040":
			out.WriteByte(' ')
		case "011":
			out.WriteByte('\t')
		case "012":
			out.WriteByte('\n')
		case "134":
			out.WriteByte('\\')
		default:
			return "", errors.New("INVALID_MOUNTINFO")
		}
		i += 3
	}
	return out.String(), nil
}

// Collect qualifies only the currently verified DSM Btrfs data-volume layout.
func Collect(ctx context.Context, mounts []Mount, previous []contracts.Volume, probe Probe, at time.Time) (Result, error) {
	result := Result{Volumes: []contracts.Volume{}, Issues: []Issue{}}
	candidates := map[string][]Mount{}
	unverified := map[string]bool{}
	for _, m := range mounts {
		if volumePath.MatchString(m.Path) {
			candidates[m.Path] = append(candidates[m.Path], m)
		}
	}
	byID := map[string][]contracts.Volume{}
	for mountPath, list := range candidates {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		code := ""
		m := list[0]
		if len(list) != 1 {
			code = "AMBIGUOUS_MOUNT"
		} else if m.Filesystem != "btrfs" || m.Root != "/@syno" {
			code = "UNSUPPORTED_VOLUME_LAYOUT"
		}
		var observed Observation
		var err error
		if code == "" {
			observed, err = probe(ctx, m)
			if err != nil {
				code = "VOLUME_UNVERIFIED"
			} else if observed.MountID != m.ID {
				code = "MOUNT_CHANGED"
			} else if !uuidPattern.MatchString(observed.UUID) || observed.UUID == "00000000-0000-0000-0000-000000000000" {
				code = "IDENTITY_UNVERIFIED"
			}
		}
		if code != "" {
			result.Issues = append(result.Issues, Issue{code, mountPath})
			unverified[mountPath] = true
			continue
		}
		filesystem := "btrfs"
		v := contracts.Volume{ID: "volume-" + observed.UUID, Name: path.Base(mountPath), MountPath: mountPath, IdentityEvidence: "BTRFS_IOC_FS_INFO:" + observed.UUID + ";mount-id:" + m.ID, Filesystem: &filesystem, State: "reachable"}
		v.Capacity = measurement(nil, at, "fstatfs.blocks*frsize")
		v.Used = measurement(nil, at, "fstatfs.(blocks-bfree)*frsize")
		v.Available = measurement(nil, at, "fstatfs.bavail*frsize")
		if observed.BlockSize == 0 || observed.Free > observed.Blocks || observed.Available > observed.Free {
			result.Issues = append(result.Issues, Issue{"CAPACITY_UNVERIFIED", mountPath})
		} else {
			v.Capacity.Value = product(observed.Blocks, observed.BlockSize)
			v.Used.Value = product(observed.Blocks-observed.Free, observed.BlockSize)
			v.Available.Value = product(observed.Available, observed.BlockSize)
		}
		byID[v.ID] = append(byID[v.ID], v)
	}
	present := map[string]bool{}
	for id, list := range byID {
		if len(list) > 1 {
			for _, v := range list {
				result.Issues = append(result.Issues, Issue{"DUPLICATE_VOLUME_ID", v.MountPath})
				unverified[v.MountPath] = true
			}
			continue
		}
		present[id] = true
		result.Volumes = append(result.Volumes, list[0])
	}
	for _, v := range previous {
		if present[v.ID] {
			continue
		}
		if !strings.HasPrefix(v.ID, "volume-") || !uuidPattern.MatchString(strings.TrimPrefix(v.ID, "volume-")) {
			return result, fmt.Errorf("INVALID_PREVIOUS_INVENTORY")
		}
		if present[v.ID] {
			continue
		}
		present[v.ID] = true
		v.State = "unmounted"
		if unverified[v.MountPath] {
			v.State = "unreadable"
		}
		v.Capacity = measurement(nil, at, "unavailable")
		v.Used = measurement(nil, at, "unavailable")
		v.Available = measurement(nil, at, "unavailable")
		result.Volumes = append(result.Volumes, v)
	}
	sort.Slice(result.Volumes, func(i, j int) bool { return result.Volumes[i].ID < result.Volumes[j].ID })
	sort.Slice(result.Issues, func(i, j int) bool {
		if result.Issues[i].MountPath == result.Issues[j].MountPath {
			return result.Issues[i].Code < result.Issues[j].Code
		}
		return result.Issues[i].MountPath < result.Issues[j].MountPath
	})
	return result, nil
}

// measurement preserves unknown values and a reproducible source/time contract.
func measurement(value *contracts.Bytes, at time.Time, method string) contracts.Measurement {
	return contracts.Measurement{Value: value, Source: "linux.fstatfs", MeasuredAt: at.UTC().Format(time.RFC3339Nano), Method: method}
}

// product avoids unsigned overflow and JavaScript's unsafe integer range.
func product(a, b uint64) *contracts.Bytes {
	v := contracts.Bytes(new(big.Int).Mul(new(big.Int).SetUint64(a), new(big.Int).SetUint64(b)).String())
	return &v
}
