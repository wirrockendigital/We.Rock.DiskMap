package index

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// ValidateSourceProvenance checks mandatory, bounded schema-10 source authority.
// Native inventory qualification remains distinct from explicit private roots and synthetic fixtures.
func ValidateSourceProvenance(m scopemanifest.ScopeManifest) error {
	p := m.Provenance
	if p.Version != 1 || (p.Authority != "native-inventory" && p.Authority != "explicit-roots" && p.Authority != "synthetic-fixture") || len(p.Roots) != len(m.Roots) || len(p.Roots) == 0 || len(p.Roots) > 1024 {
		return ErrGeneration
	}
	raw, e := json.Marshal(p)
	if e != nil || len(raw) > 1024*1024 {
		return ErrGeneration
	}
	roots := map[string]scopemanifest.ManifestRoot{}
	for _, r := range m.Roots {
		if _, ok := roots[r.TargetID]; ok {
			return ErrGeneration
		}
		roots[r.TargetID] = r
	}
	seen := map[string]bool{}
	sources := map[string]bool{}
	covered := map[string]bool{}
	for _, id := range m.CoveredTargetIDs {
		covered[id] = true
	}
	areas := 0
	for _, r := range p.Roots {
		original, ok := roots[r.TargetID]
		if !ok || seen[r.TargetID] || r.VolumeID != original.VolumeID || r.Kind != original.Kind || !reflect.DeepEqual(r.Binding, original.Binding) || !provenanceName(r.Name) || !provenanceName(r.VolumeName) || r.SourceID == nil || *r.SourceID != r.TargetID || r.Areas == nil {
			return ErrGeneration
		}
		seen[r.TargetID] = true
		if sources[r.TargetID] {
			return ErrGeneration
		}
		sources[r.TargetID] = true
		ids := map[string]bool{}
		paths := map[string]bool{}
		for _, a := range r.Areas {
			areas++
			if areas > 2048 || p.Authority != "native-inventory" || r.Kind != "volume" || a.VolumeID != r.VolumeID || !provenanceName(a.Name) {
				return ErrGeneration
			}
			if a.SourceID != nil {
				if !security.ValidID(*a.SourceID) || !covered[*a.SourceID] || ids[*a.SourceID] || sources[*a.SourceID] {
					return ErrGeneration
				}
				ids[*a.SourceID] = true
				sources[*a.SourceID] = true
			}
			if a.RelativePath != nil {
				rel := string(a.RelativePath)
				if rel == "." || security.RelativeBytes(rel) != nil || paths[rel] {
					return ErrGeneration
				}
				paths[rel] = true
			}
			switch a.State {
			case "reachable", "missing", "unreadable", "locked", "unmounted", "unverified", "unavailable":
			default:
				return ErrGeneration
			}
			switch a.Qualification {
			case "unavailable":
				if a.Binding != nil {
					return ErrGeneration
				}
			case "qualified":
				if a.SourceID == nil || a.Binding == nil || r.Binding == nil || a.State != "reachable" || a.RelativePath == nil || !utf8.ValidString(a.Binding.Path) || a.Binding.Device == "" || a.Binding.Inode == "" || a.Binding.MountID == "" || a.Binding.Path != filepath.Join(original.PhysicalPath, string(a.RelativePath)) {
					return ErrGeneration
				}
				authorized := a.Binding.Device == r.Binding.Device && a.Binding.MountID == r.Binding.MountID
				for _, q := range m.QualifiedSubvolumes {
					if q.TargetID == r.TargetID && q.SourceID == *a.SourceID && bytes.Equal([]byte(q.RelativePath), a.RelativePath) && reflect.DeepEqual(q.Binding, *a.Binding) {
						authorized = true
					}
				}
				if !authorized {
					return ErrGeneration
				}
			default:
				return ErrGeneration
			}
		}
	}
	if p.Authority == "native-inventory" {
		for id := range covered {
			// Owner-scoped NAS scans cover volume navigation through qualified share roots.
			container := false
			for _, root := range p.Roots {
				if root.Kind == "share" && root.VolumeID == id {
					container = true
					break
				}
			}
			if !sources[id] && !container {
				return ErrGeneration
			}
		}
	}
	return nil
}

// provenanceName bounds public historical labels independently of raw path identity.
func provenanceName(s string) bool {
	return len(s) <= 4096 && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// validateProvenanceRows binds native observed boundaries to the actual captured directory metadata.
// An absent boundary needs a recorded gap or exclusion; its capture never claims that its contents were read.
func validateProvenanceRows(ctx context.Context, tx *sql.Tx, h GenerationHeader) error {
	if err := ValidateSourceProvenance(h.Manifest); err != nil {
		return err
	}
	if h.Manifest.Provenance.Authority != "native-inventory" {
		return nil
	}
	for _, r := range h.Manifest.Provenance.Roots {
		// Native root identity must agree with the metadata actually traversed.
		if r.Binding != nil {
			v, err := findNode(ctx, tx, r.TargetID, []byte("."))
			identity := v.ObjectIdentity

			if err == nil {
				parts := strings.Split(identity, ":")
				if len(parts) != 4 || parts[0] != r.Binding.Device || parts[1] != r.Binding.Inode {
					return ErrGeneration
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			} else {
				var gap int
				if err = tx.QueryRowContext(ctx, `SELECT 1 FROM scan_boundaries WHERE target_id=? AND path_bytes=CAST('.' AS BLOB) LIMIT 1`, r.TargetID).Scan(&gap); err != nil {
					return ErrGeneration
				}
			}
		}
		for _, a := range r.Areas {
			if a.Qualification != "qualified" {
				continue
			}
			v, err := findNode(ctx, tx, r.TargetID, a.RelativePath)
			kind, identity := v.Node.Kind, v.ObjectIdentity

			if err == nil {
				parts := strings.Split(identity, ":")
				if kind != "directory" || len(parts) != 4 || parts[0] != a.Binding.Device || parts[1] != a.Binding.Inode {
					return ErrGeneration
				}
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			// The exact source gap may be represented on the covered share or on a raw ancestor.
			rows, e := tx.QueryContext(ctx, `SELECT target_id,path_bytes FROM scan_boundaries WHERE target_id=? OR target_id=?`, r.TargetID, *a.SourceID)
			if e != nil {
				return e
			}
			explained := false
			for rows.Next() {
				var target string
				var p []byte
				if e = rows.Scan(&target, &p); e != nil {
					break
				}
				if target == *a.SourceID || bytes.Equal(p, []byte(".")) || bytes.Equal(p, a.RelativePath) || bytes.HasPrefix(a.RelativePath, append(append([]byte{}, p...), '/')) {
					explained = true
				}
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
			if e != nil {
				return e
			}
			if !explained {
				return ErrGeneration
			}
		}
	}
	return nil
}
