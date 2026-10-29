package inventory

import (
	"bytes"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/sharepolicy"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// ReadShareUUID opens only DSM's UUID metadata; encryption marker contents are never read.
func ReadShareUUID(volumePath, name string) (string, error) {
	if !shareName(name) {
		return "", errShareRegistry
	}
	root, _, err := scanner.OpenBoundRoot(filepath.Join(volumePath, "@eaDir", name))
	if err != nil {
		return "", errShareRegistry
	}
	defer root.Close()
	// Openat enforces no-follow at the kernel boundary, including the final metadata file.
	directory, err := root.Open(".")
	if err != nil {
		return "", errShareRegistry
	}
	defer directory.Close()
	fd, err := unix.Openat(int(directory.Fd()), "SYNO@.attr", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", errShareRegistry
	}
	file := os.NewFile(uintptr(fd), "SYNO@.attr")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", errShareRegistry
	}
	return ParseShareUUID(file)
}

// ParseShareUUID accepts one bounded UUID object, rejecting duplicate fields and trailing documents.
func ParseShareUUID(reader io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(raw) > 4096 || !utf8.Valid(raw) {
		return "", errShareRegistry
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", errShareRegistry
	}
	token, err = decoder.Token()
	if err != nil || token != "uuid" {
		return "", errShareRegistry
	}
	var uuid string
	if decoder.Decode(&uuid) != nil || !uuidPattern.MatchString(uuid) || uuid == "00000000-0000-0000-0000-000000000000" {
		return "", errShareRegistry
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return "", errShareRegistry
	}
	if _, err = decoder.Token(); err != io.EOF {
		return "", errShareRegistry
	}
	return uuid, nil
}

// ShareRights exposes the shared native policy to inventory consumers.
type ShareRights = sharepolicy.Rights

// ParseShareRights preserves the inventory error boundary for malformed policy.
func ParseShareRights(reader io.Reader) (map[string]ShareRights, error) {
	result, err := sharepolicy.Parse(reader)
	if err != nil {
		return nil, errShareRegistry
	}
	return result, nil
}
