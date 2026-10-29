package jobs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// captureSourceProvenance uses the server-owned start catalog and descriptor-qualified roots.
// It does not accept provenance flags or private paths from a public profile DTO.
func captureSourceProvenance(ctx context.Context, manifest *scopemanifest.ScopeManifest, sources profileSources) error {
	p := scopemanifest.ExplicitProvenance(*manifest, false)
	if sources.Mode != "native_inventory" {
		manifest.Provenance = p
		return nil
	}
	p.Authority = "native-inventory"
	for i, r := range manifest.Roots {
		out := &p.Roots[i]
		for _, v := range sources.Volumes {
			if v.ID == r.VolumeID {
				out.VolumeName = v.Name
				if r.Kind == "volume" {
					out.Name = v.Name
				}
				break
			}
		}
		if r.Kind == "share" {
			for _, s := range sources.Shares {
				if s.ID == r.TargetID && s.VolumeID == r.VolumeID {
					out.Name = s.Name
					if r.RelativePath != "" {
						out.Name += "/" + r.RelativePath
					}
					break
				}
			}
			continue
		}
		// Hold the captured volume while checking each catalog boundary against it.
		var held *os.Root
		if r.Binding != nil {
			d, b, e := scanner.OpenBoundRoot(r.PhysicalPath)
			if e == nil {
				if b.Path == r.Binding.Path && b.Device == r.Binding.Device && b.Inode == r.Binding.Inode && b.MountID == r.Binding.MountID {
					held = d
				} else {
					d.Close()
				}
			}
		}
		for _, s := range sources.Shares {
			if err := ctx.Err(); err != nil {
				if held != nil {
					held.Close()
				}
				return err
			}
			if s.VolumeID != r.VolumeID {
				continue
			}
			id := s.ID
			a := scopemanifest.SourceArea{SourceID: &id, VolumeID: s.VolumeID, Name: s.Name, State: s.State, Qualification: "unavailable"}
			// Catalog membership alone does not qualify a path outside the captured volume.
			rel, err := filepath.Rel(r.PhysicalPath, s.PhysicalPath)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
				a.RelativePath = []byte(filepath.ToSlash(rel))
				if held != nil && r.Binding != nil && s.State == "reachable" && s.Readable && (s.Locked == nil || !*s.Locked) {
					descriptor, b, e := scanner.OpenBoundRoot(s.PhysicalPath)
					if e == nil {
						observed, observedErr := descriptor.Stat(".")
						anchored, anchorErr := held.Stat(rel)
						descriptor.Close()
						same := b.Device == r.Binding.Device && b.MountID == r.Binding.MountID
						for _, q := range manifest.QualifiedSubvolumes {
							if q.TargetID == r.TargetID && q.SourceID == s.ID && q.RelativePath == string(a.RelativePath) && q.Binding.Device == b.Device && q.Binding.Inode == b.Inode && q.Binding.MountID == b.MountID {
								same = true
							}
						}
						if same && observedErr == nil && anchorErr == nil && os.SameFile(observed, anchored) && utf8.ValidString(b.Path) {
							a.Binding = &scopemanifest.RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}
							a.Qualification = "qualified"
						}
					}
				}
			}
			out.Areas = append(out.Areas, a)
		}
		if held != nil {
			held.Close()
		}
	}
	manifest.Provenance = p
	return nil
}
