package sharepolicy

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const registry = "/usr/syno/etc/share_right.map"

// Snapshot binds the visibility flags to one unchanged native policy observation.
type Snapshot struct {
	Rights map[string]Rights
	digest [32]byte
}

// Read loads the bounded native registry without retaining unrelated source contents.
func Read() (Snapshot, error) {
	file, err := os.Open(registry)
	if err != nil {
		return Snapshot{}, ErrPolicy
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return Snapshot{}, ErrPolicy
	}
	rights, err := Parse(bytes.NewReader(raw))
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Rights: rights, digest: sha256.Sum256(raw)}, nil
}

// ForRoot refuses volume roots and unqualified permission modes before traversal.
func (s Snapshot) ForRoot(root string) (Rights, error) {
	if filepath.Clean(root) != root {
		return Rights{}, ErrPolicy
	}
	var found *Rights
	for _, p := range s.Rights {
		if root == p.Path || strings.HasPrefix(root, p.Path+"/") {
			if found != nil || !p.ACL || !p.SkipSMB || p.ListDisabled || p.Locked {
				return Rights{}, ErrPolicy
			}
			copy := p
			found = &copy
		}
	}
	if found == nil {
		return Rights{}, ErrPolicy
	}
	return *found, nil
}

// Check rejects a changed registry instead of publishing metadata under old visibility rules.
func (s Snapshot) Check() error {
	now, err := Read()
	if err != nil || now.digest != s.digest {
		return ErrPolicy
	}
	return nil
}
