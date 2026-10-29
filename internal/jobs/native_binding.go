package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

var bootUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validBoot excludes absent, malformed and synthetic zero boot identities.
func validBoot(id string) bool {
	return bootUUID.MatchString(id) && id != "00000000-0000-0000-0000-000000000000"
}

// nativeBoot reads only the kernel's bounded boot identity, never caller input.
func nativeBoot() (string, error) {
	f, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", ErrUnavailable
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 38))
	if err != nil {
		return "", ErrUnavailable
	}
	id := strings.TrimSuffix(string(raw), "\n")
	if !validBoot(id) {
		return "", ErrUnavailable
	}
	return id, nil
}

// bindingEpochs copies the per-root provenance so partial captures cannot advance an unbound root.
func bindingEpochs(old map[string]string) map[string]string {
	out := map[string]string{}
	for id, boot := range old {
		out[id] = boot
	}
	return out
}

// stableNativeRoot qualifies UUID provenance and mappings independently from ephemeral mount IDs.
func stableNativeRoot(before, after profileSources, r inventory.NormalizedRoot) bool {
	if before.Mode != "native_inventory" || after.Mode != "native_inventory" {
		return false
	}
	volume := func(s profileSources) (contracts.Volume, bool) {
		var out contracts.Volume
		count := 0
		for _, v := range s.Volumes {
			if v.ID == r.VolumeID {
				out = v
				count++
			}
		}
		return out, count == 1
	}
	a, ok := volume(before)
	b, ok2 := volume(after)
	uuid := strings.TrimPrefix(r.VolumeID, "volume-")
	if !ok || !ok2 || !strings.HasPrefix(r.VolumeID, "volume-") || !validBoot(uuid) || a.MountPath != b.MountPath || b.State != "reachable" || a.Filesystem == nil || b.Filesystem == nil || *a.Filesystem != *b.Filesystem || !strings.HasPrefix(a.IdentityEvidence, "BTRFS_IOC_FS_INFO:"+uuid+";") || !strings.HasPrefix(b.IdentityEvidence, "BTRFS_IOC_FS_INFO:"+uuid+";") {
		return false
	}
	if r.Kind == "volume" {
		return r.TargetID == r.VolumeID && r.PhysicalPath == b.MountPath
	}
	share := func(s profileSources) (contracts.Share, bool) {
		var out contracts.Share
		count := 0
		for _, v := range s.Shares {
			if v.ID == r.TargetID {
				out = v
				count++
			}
		}
		return out, count == 1
	}
	x, ok := share(before)
	y, ok2 := share(after)
	uuid = strings.TrimPrefix(r.TargetID, "share-")
	return ok && ok2 && strings.HasPrefix(r.TargetID, "share-") && validBoot(uuid) && x.IdentityEvidence == "DSM90080:SYNO@.attr:"+uuid && y.IdentityEvidence == x.IdentityEvidence && x.VolumeID == r.VolumeID && y.VolumeID == r.VolumeID && x.PhysicalPath == y.PhysicalPath && filepath.Join(x.PhysicalPath, r.RelativePath) == r.PhysicalPath && x.FileStationPath != nil && y.FileStationPath != nil && *x.FileStationPath == *y.FileStationPath && observedBool(x.Encrypted) == observedBool(y.Encrypted) && y.State == "reachable" && y.Readable && y.Locked != nil && !*y.Locked
}

// mayRebind permits only a proved boot transition, or an explicit recovery of missing provenance.
func mayRebind(before, after profileSources, r inventory.NormalizedRoot, explicit bool) bool {
	old := before.BindingBoots[r.TargetID]
	signature := before.BindingIdentities[r.TargetID]
	current := nativeRootIdentity(after, r)
	return validBoot(after.BootID) && ((validBoot(old) && old != after.BootID && signature != "" && signature == current) || (explicit && old == "" && (signature == "" || signature == current))) && stableNativeRoot(before, after, r)
}

// confirmNativeBindings closes catalog/descriptor races before publishing any new baseline.
func confirmNativeBindings(ctx context.Context, p contracts.Profile, s profileSources, bindings map[string]scanner.RootBinding, read catalogReader) error {
	if s.Mode != "native_inventory" || !validBoot(s.BootID) {
		return nil
	}
	fresh, err := read(ctx, s)
	if err != nil || fresh.BootID != s.BootID {
		return ErrRootRebind
	}
	n, err := normalizeSources(p.Scope, s)
	if err != nil {
		return ErrRootRebind
	}
	for _, r := range n.Roots {
		expected, ok := bindings[r.TargetID]
		if !ok || r.State != "reachable" {
			continue
		}
		if !stableNativeRoot(s, fresh, r) || !sameCapturedMount(s, fresh, r.TargetID) {
			return ErrRootRebind
		}
		fd, b, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return ErrRootRebind
		}
		fd.Close()
		if b != expected {
			return ErrRootRebind
		}
	}
	// Re-read boot and mapping after opening descriptors; a reboot during binding is never accepted.
	end, err := read(ctx, s)
	if err != nil || end.BootID != s.BootID {
		return ErrRootRebind
	}
	for _, r := range n.Roots {
		if _, ok := bindings[r.TargetID]; ok && r.State == "reachable" && (!stableNativeRoot(s, end, r) || !sameCapturedMount(s, end, r.TargetID)) {
			return ErrRootRebind
		}
	}
	return nil
}

// RequalifyNativeProfile is an explicit private recovery boundary for pre-provenance profiles.
// It never changes source selection or rewrites already captured jobs and generations.
func (m *Manager) RequalifyNativeProfile(ctx context.Context, id string, expected int) (contracts.Profile, error) {
	var empty contracts.Profile
	if !identifier.MatchString(id) || expected < 1 {
		return empty, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return empty, ErrUnavailable
	}
	count, err := m.queueCount()
	if err != nil {
		return empty, err
	}
	if m.active != nil || count > 0 {
		return empty, ErrConflict
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	old, err := profiles.Read(ctx, tx, id)
	if err != nil {
		return empty, profileError(err)
	}
	if old.Profile.Revision != expected {
		return empty, ErrConflict
	}
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT sources_json FROM profile_sources WHERE profile_id=?`, id).Scan(&raw); err != nil {
		return empty, err
	}
	previous, err := loadSources(raw)
	if err != nil {
		return empty, err
	}
	if previous.Mode != "native_inventory" {
		return empty, ErrInvalid
	}
	var bindings map[string]scanner.RootBinding
	if json.Unmarshal([]byte(old.RootsJSON), &bindings) != nil || bindings == nil {
		return empty, ErrUnavailable
	}
	capture, err := captureScopeWithRecovery(ctx, old.Profile, previous, bindings, m.state, m.catalog, true)
	if err != nil {
		return empty, err
	}
	if len(capture.Manifest.Issues) > 0 || len(capture.Manifest.Roots) == 0 {
		return empty, ErrRootRebind
	}
	for _, r := range capture.Manifest.Roots {
		if !validBoot(capture.Sources.BindingBoots[r.TargetID]) {
			return empty, ErrRootRebind
		}
	}
	roots, _ := json.Marshal(capture.Bindings)
	sources, _ := json.Marshal(capture.Sources)
	known := map[string]bool{}
	for _, id := range capture.Manifest.CoveredTargetIDs {
		known[id] = true
	}
	next := old.Profile
	next.Revision++
	if next.Revision <= expected {
		return empty, ErrInvalid
	}
	err = profiles.Import(ctx, tx, profiles.Record{Profile: next, RootsJSON: string(roots), VolumesJSON: old.VolumesJSON}, known)
	if err != nil {
		return empty, profileError(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(sources), id); err != nil {
		return empty, err
	}
	if err = tx.Commit(); err != nil {
		return empty, err
	}
	return next, nil
}

// nativeRootIdentity snapshots only verified stable relationships, not observations or mount IDs.
func nativeRootIdentity(s profileSources, r inventory.NormalizedRoot) string {
	if !stableNativeRoot(s, s, r) {
		return ""
	}
	fields := []string{r.Kind, r.TargetID, r.VolumeID, r.PhysicalPath}
	for _, v := range s.Volumes {
		if v.ID == r.VolumeID {
			fields = append(fields, v.MountPath, *v.Filesystem)
		}
	}
	if r.Kind == "share" {
		for _, v := range s.Shares {
			if v.ID == r.TargetID {
				fields = append(fields, *v.FileStationPath, observedBool(v.Encrypted))
			}
		}
	}
	raw, _ := json.Marshal(fields)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// stampNativeBindings couples fresh root descriptors to their verified boot and stable identity.
func stampNativeBindings(s *profileSources, p contracts.Profile, bindings map[string]scanner.RootBinding) {
	s.BindingMounts = copyMountProofs(nil)
	s.BindingBoots = bindingEpochs(nil)
	s.BindingIdentities = bindingEpochs(nil)
	if !validBoot(s.BootID) {
		return
	}
	n, err := normalizeSources(p.Scope, *s)
	if err != nil {
		return
	}
	for _, r := range n.Roots {
		if _, ok := bindings[r.TargetID]; ok {
			s.BindingBoots[r.TargetID] = s.BootID
			s.BindingIdentities[r.TargetID] = nativeRootIdentity(*s, r)
			stampMountProof(s, r, bindings[r.TargetID])
		}
	}
}
