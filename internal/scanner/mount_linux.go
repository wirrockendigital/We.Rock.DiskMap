//go:build linux

package scanner

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// MountIdentity identifies the held mount, distinguishing same-device bind mounts.
func MountIdentity(file *os.File) (string, error) {
	input, err := os.Open("/proc/self/fdinfo/" + strconv.FormatUint(uint64(file.Fd()), 10))
	if err != nil {
		return "", err
	}
	defer input.Close()
	raw, err := io.ReadAll(io.LimitReader(input, 16385))
	if err != nil || len(raw) > 16384 {
		return "", fmt.Errorf("mount identity unavailable")
	}
	id := ""
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "mnt_id:" {
			if id != "" {
				return "", fmt.Errorf("ambiguous mount identity")
			}
			if _, err = strconv.ParseUint(f[1], 10, 64); err != nil {
				return "", err
			}
			id = f[1]
		}
	}
	if id == "" {
		return "", fmt.Errorf("mount identity unavailable")
	}
	return id, nil
}
