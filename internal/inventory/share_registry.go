package inventory

import (
	"bufio"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

var errShareRegistry = errors.New("SHARE_REGISTRY_UNAVAILABLE")

// ParseShares accepts only explicit unique section/path mappings from DSM's generated share file.
func ParseSharePaths(reader io.Reader) (map[string]string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 || !utf8.Valid(raw) {
		return nil, errShareRegistry
	}
	out := map[string]string{}
	seen := map[string]bool{}
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	scanner.Buffer(make([]byte, 4096), 64*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return nil, errShareRegistry
			}
			section = strings.TrimSpace(line[1 : len(line)-1])
			fold := strings.ToLower(section)
			if section == "" || section == "global" || strings.ContainsAny(section, "/\\\x00\r\n") || seen[fold] {
				return nil, errShareRegistry
			}
			seen[fold] = true
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section == "" {
			return nil, errShareRegistry
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.EqualFold(key, "include") || strings.EqualFold(key, "copy") {
			return nil, errShareRegistry
		}
		if strings.EqualFold(key, "path") {
			if _, ok := out[section]; ok || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsAny(value, "\"\x00\r\n%") {
				return nil, errShareRegistry
			}
			out[section] = value
		}
	}
	if scanner.Err() != nil || len(out) != len(seen) {
		return nil, errShareRegistry
	}
	paths := map[string]bool{}
	for _, p := range out {
		if paths[p] {
			return nil, errShareRegistry
		}
		paths[p] = true
	}
	return out, nil
}

// internalShareRegistry excludes DSM's persistent USB records from the internal-volume contract.
// USB records can survive unplugging and use device identifiers rather than share names.
// No external path becomes scannable; all internal mappings still require exact agreement.
func internalShareRegistry(mappings map[string]string, rights map[string]ShareRights) (map[string]string, map[string]ShareRights, error) {
	isUSB := func(value string) bool {
		parts := strings.Split(value, "/")
		if len(parts) != 3 || !strings.HasPrefix(parts[1], "volumeUSB") || parts[2] == "" {
			return false
		}
		number := strings.TrimPrefix(parts[1], "volumeUSB")
		if number == "" || number[0] == '0' {
			return false
		}
		for _, digit := range number {
			if digit < '0' || digit > '9' {
				return false
			}
		}
		return strings.HasPrefix(parts[2], "usbshare") && filepath.Clean(value) == value
	}
	nativeMappings := make(map[string]string)
	nativeRights := make(map[string]ShareRights)
	for name, value := range mappings {
		if !isUSB(value) {
			nativeMappings[name] = value
		}
	}
	for name, value := range rights {
		if !isUSB(value.Path) {
			nativeRights[name] = value
		}
	}
	for name, value := range nativeMappings {
		right, ok := nativeRights[name]
		if !ok || right.Path != value || right.Locked {
			return nil, nil, errShareRegistry
		}
	}
	for name, right := range nativeRights {
		value, ok := nativeMappings[name]
		if (!right.Locked && (!ok || value != right.Path)) || (right.Locked && ok) {
			return nil, nil, errShareRegistry
		}
	}
	return nativeMappings, nativeRights, nil
}
