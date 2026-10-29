package scanner

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// Scan writes a metadata generation under the daemon's unchanged operating identity.
func Scan(ctx context.Context, db *sql.DB, snapshot string, profile contracts.Profile, roots map[string]RootBinding, volumes map[string]string) (Result, error) {
	// Direct scanner tests provide an already disjoint explicit fixture selection.
	manifest := scopemanifest.ScopeManifest{ContractVersion: scopemanifest.ScopeManifestVersion, Kind: profile.Scope.Kind, Roots: []scopemanifest.ManifestRoot{}}
	for _, target := range profile.Scope.TargetIDs {
		b := roots[target]
		r := scopemanifest.ManifestRoot{NormalizedRoot: scopemanifest.NormalizedRoot{TargetID: target, Kind: "volume", VolumeID: volumes[target], PhysicalPath: b.Path, State: "reachable"}, Binding: &scopemanifest.RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}}
		if profile.Scope.Kind == "shares" {
			r.Kind = "share"
			id := target
			r.ShareID = &id
		}
		manifest.Roots = append(manifest.Roots, r)
	}
	return ScanManifest(ctx, db, snapshot, profile, manifest, index.DefaultFileTypeMapping())
}
