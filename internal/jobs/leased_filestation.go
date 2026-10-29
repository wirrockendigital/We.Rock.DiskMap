package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsm/filestation"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ReadLeasedFileStation resolves the selected historical object without adopting a newer generation.
// The lease authorizes metadata access, not filesystem identity: current scope and object checks remain mandatory.
func (m *Manager) ReadLeasedFileStation(parent context.Context, owner string, in LeasedPathsRequest, shares map[string]string) (FileStationTarget, error) {
	var out FileStationTarget
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	release, err := m.reserveHierarchy(ctx, owner, in.hierarchy())
	if err != nil {
		return out, err
	}
	defer release()
	db, close, err := m.generationDB(in.SnapshotID)
	if err != nil {
		return out, err
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, in.SnapshotID)
	if err != nil {
		return out, ErrGenerationRefresh
	}
	// A sealed partial generation can resolve known nodes with the same live authorization checks.
	if header.Profile.ID != in.ProfileID || (header.State != "complete" && header.State != "partial") {
		return out, ErrStaleSnapshot
	}
	// Numeric locators are resolved only inside the already authorized sealed snapshot.
	record, err := index.ReadNode(ctx, db, in.NodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, ErrUnavailable
	}
	node, target, path, identity := record.Node, record.TargetID, record.Path, record.ObjectIdentity
	if node.Kind != "file" && node.Kind != "directory" {
		return out, ErrNotFound
	}
	var binding scanner.RootBinding
	matched := 0
	for _, root := range header.Manifest.Roots {
		if root.TargetID == target {
			if root.Binding == nil || root.PhysicalPath != root.Binding.Path || security.ValidateNode(node, in.SnapshotID, in.NodeID, target, root.VolumeID, string(path)) != nil {
				return out, ErrNotFound
			}
			binding = scanner.RootBinding{Path: root.Binding.Path, Device: root.Binding.Device, Inode: root.Binding.Inode, MountID: root.Binding.MountID}
			matched++
		}
	}
	if matched != 1 || binding.MountID == "" {
		return out, ErrRootRebind
	}
	if !utf8.Valid(path) {
		return out, ErrPathEncoding
	}
	// Read current profile bindings under lifecycle serialization, then release it before source I/O.
	currentAttempt := func() error {
		// Acquire the observation connection before entering lifecycle serialization.
		conn, err := m.readDB.Conn(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		defer conn.Close()
		if err := m.lockQuery(ctx); err != nil {
			return err
		}
		defer m.mu.Unlock()
		if m.closed {
			return ErrUnavailable
		}
		var rootsRaw, volumesRaw string
		var revision int
		if err := conn.QueryRowContext(ctx, `SELECT revision,roots_json,volumes_json FROM profiles WHERE id=?`, in.ProfileID).Scan(&revision, &rootsRaw, &volumesRaw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrProfileMissing
			}
			return fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		var roots map[string]scanner.RootBinding
		var volumes map[string]string
		if json.Unmarshal([]byte(rootsRaw), &roots) != nil || json.Unmarshal([]byte(volumesRaw), &volumes) != nil {
			return ErrUnavailable
		}
		if revision != header.Profile.Revision || roots[target] != binding || volumes[target] != node.VolumeID {
			return ErrStaleSnapshot
		}
		if err := m.generationIntegrityAllowed(in.SnapshotID); err != nil {
			return err
		}
		m.readerMu.Lock()
		defer m.readerMu.Unlock()
		lease := m.viewLeases[in.ViewID]
		if lease == nil || lease.Released || !m.viewNow().Before(lease.Expires) {
			return ErrViewExpired
		}
		if lease.Owner != owner || lease.Profile != in.ProfileID || lease.Snapshot != in.SnapshotID {
			return ErrNotFound
		}
		return nil
	}
	// Busy retries release the observation connection and lifecycle lock first.
	current := func() error {
		_, err := retryControlRead(ctx, func() (struct{}, error) { return struct{}{}, currentAttempt() })
		return err
	}
	if err = current(); err != nil {
		return out, err
	}
	lookup := func(relative string) (string, error) {
		record, err := index.FindNode(ctx, db, target, []byte(relative))
		return record.ObjectIdentity, err
	}
	out, err = filestation.Resolve(ctx, binding, string(path), node.Kind, identity, shares, lookup)
	if err != nil {
		return FileStationTarget{}, ErrFileStationUnavailable
	}
	// A release, profile edit or expiry during resolution cannot publish a late target.
	if err = current(); err != nil {
		return FileStationTarget{}, err
	}
	if ctx.Err() != nil {
		return FileStationTarget{}, ErrUnavailable
	}
	return out, nil
}
