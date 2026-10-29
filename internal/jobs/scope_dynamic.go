package jobs

import (
	"context"
	"database/sql"
	"sort"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// sourceObservation compares native relationships separately from descriptor-bound root objects.
type sourceObservation struct {
	kind, volume, path, state string
	readable                  bool
	encrypted, locked         string
}

// observations retains exact registry identities; names never participate in matching.
func observations(s profileSources) map[string]sourceObservation {
	out := map[string]sourceObservation{}
	for _, v := range s.Volumes {
		out[v.ID] = sourceObservation{kind: "volume", volume: v.ID, path: v.MountPath, state: v.State}
	}
	for _, v := range s.Shares {
		o := sourceObservation{kind: "share", volume: v.VolumeID, path: v.PhysicalPath, state: v.State, readable: v.Readable}
		o.encrypted = observedBool(v.Encrypted)
		o.locked = observedBool(v.Locked)
		out[v.ID] = o
	}
	for _, r := range s.Qualified {
		out[r.TargetID] = sourceObservation{kind: r.Kind, volume: r.VolumeID, path: r.PhysicalPath, state: r.State}
	}
	return out
}

// observedBool preserves unknown separately from explicit false.
func observedBool(v *bool) string {
	if v == nil {
		return "unknown"
	}
	if *v {
		return "true"
	}
	return "false"
}

// validateScopeEnd creates separate end evidence and never modifies the run-start manifest.
func validateScopeEnd(ctx context.Context, p contracts.Profile, manifest inventory.ScopeManifest, started profileSources, read catalogReader) ([]index.EndValidationGap, bool) {
	gaps := []index.EndValidationGap{}
	seen := map[string]bool{}
	add := func(root, source, code string) {
		key := root + "\x00" + source + "\x00" + code
		if !seen[key] {
			seen[key] = true
			gaps = append(gaps, index.EndValidationGap{TargetID: root, SourceID: source, Code: code})
		}
	}
	if started.Mode == "native_inventory" {
		current, err := read(ctx, started)
		if err == nil && validBoot(started.BootID) && current.BootID != started.BootID {
			for _, r := range manifest.Roots {
				add(r.TargetID, r.TargetID, "SCOPE_CHANGED")
			}
		}
		if err != nil {
			for _, r := range manifest.Roots {
				add(r.TargetID, r.TargetID, "SCOPE_VALIDATION_UNAVAILABLE")
			}
		} else {
			current = retainScopeIdentities(started, current)
			if _, err = normalizeSources(p.Scope, current); err != nil {
				for _, r := range manifest.Roots {
					add(r.TargetID, r.TargetID, "SCOPE_VALIDATION_UNAVAILABLE")
				}
			} else {
				before, after := observations(started), observations(current)
				for _, id := range manifest.CoveredTargetIDs {
					old, existed := before[id]
					fresh, exists := after[id]
					if existed && exists && old == fresh && sameCapturedMount(started, current, id) {
						continue
					}
					for _, r := range manifest.Roots {
						if r.TargetID == id || r.Kind == "volume" && old.volume == r.VolumeID {
							add(r.TargetID, id, "SCOPE_CHANGED")
						}
					}
				}
			}
		}
	}
	// A root checked early by its traversal may change while a later root is still scanning.
	for _, r := range manifest.Roots {
		if r.Binding == nil {
			continue
		}
		fd, current, err := inventory.OpenScopeRoot(r.NormalizedRoot)
		if err == nil {
			fd.Close()
		}
		expected := r.Binding
		if err != nil || current.Path != expected.Path || current.Device != expected.Device || current.Inode != expected.Inode || current.MountID != expected.MountID {
			add(r.TargetID, r.TargetID, "SCOPE_CHANGED")
		}
	}
	// Qualified share areas also retain descriptor identity on non-subvolume filesystems.
	for _, r := range manifest.Provenance.Roots {
		for _, area := range r.Areas {
			if ctx.Err() != nil {
				return gaps, true
			}
			if area.Qualification != "qualified" || area.Binding == nil || area.SourceID == nil {
				continue
			}
			expected := area.Binding
			fd, current, err := scanner.OpenBoundRoot(expected.Path)
			if err == nil {
				fd.Close()
			}
			if err != nil || current.Path != expected.Path || current.Device != expected.Device || current.Inode != expected.Inode || current.MountID != expected.MountID {
				add(r.TargetID, *area.SourceID, "SCOPE_CHANGED")
			}
		}
	}
	// Captured registry subvolumes retain their own identity beneath a stable volume root.
	for _, q := range manifest.QualifiedSubvolumes {
		expected := q.Binding
		root, current, err := scanner.OpenBoundRoot(expected.Path)
		if err != nil {
			add(q.TargetID, q.SourceID, "SCOPE_CHANGED")
			continue
		}
		file, err := root.Open(".")
		if err != nil {
			root.Close()
			add(q.TargetID, q.SourceID, "SCOPE_VALIDATION_UNAVAILABLE")
			continue
		}
		tree, btrfs, err := scanner.NativeSubvolumeID(file)
		file.Close()
		root.Close()
		if current.Path != expected.Path || current.Device != expected.Device || current.Inode != expected.Inode || current.MountID != expected.MountID {
			add(q.TargetID, q.SourceID, "SCOPE_CHANGED")
		} else if err != nil || !btrfs {
			add(q.TargetID, q.SourceID, "SCOPE_VALIDATION_UNAVAILABLE")
		} else if tree != q.TreeID {
			add(q.TargetID, q.SourceID, "SCOPE_CHANGED")
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		a, b := gaps[i], gaps[j]
		if a.TargetID != b.TargetID {
			return a.TargetID < b.TargetID
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.Code < b.Code
	})
	return gaps, ctx.Err() != nil
}

// recordScopeEnd updates only the unsealed staging generation and returns measured added gaps.
func recordScopeEnd(ctx context.Context, db *sql.DB, gaps []index.EndValidationGap) error {
	if len(gaps) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, gap := range gaps {
		// End observations are separate durable records; the captured start manifest is immutable.
		if err = index.RecordBoundary(ctx, tx, gap.TargetID, ".", gap.Code); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO coverage(target_id,state,errors) VALUES(?,'partial',1) ON CONFLICT(target_id) DO UPDATE SET state='partial',errors=coverage.errors+1`, gap.TargetID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
