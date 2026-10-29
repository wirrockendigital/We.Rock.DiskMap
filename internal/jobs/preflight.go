package jobs

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"strconv"
	"syscall"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
	"modernc.org/sqlite"
)

// IndexReserveBytes is a minimum operational reserve, not an estimate of total scan storage.
const IndexReserveBytes uint64 = 64 << 20

// availableStateBytes measures the actual package-state filesystem, not source volume capacity.
func (m *Manager) availableStateBytes() (uint64, error) {
	if m.spaceProbe != nil {
		return m.spaceProbe()
	}
	var s unix.Statfs_t
	if err := unix.Statfs(m.state, &s); err != nil {
		return 0, err
	}
	block := uint64(s.Bsize)
	if block == 0 {
		return 0, ErrUnavailable
	}
	if uint64(s.Bavail) > math.MaxUint64/block {
		return math.MaxUint64, nil
	}
	return uint64(s.Bavail) * block, nil
}

// reserve rejects unavailable measurements rather than inventing writable capacity.
func (m *Manager) reserve() error {
	free, err := m.availableStateBytes()
	if err != nil {
		return errors.New("INDEX_SPACE_UNAVAILABLE")
	}
	if free < IndexReserveBytes {
		return errors.New("INDEX_SPACE_LOW")
	}
	return nil
}

// preflight validates captured intent; later profile edits never rewrite an accepted job.
func (m *Manager) preflight(o *Outcome) error { return m.preflightContext(m.ctx, o) }

// preflightContext observes the same cooperative boundaries as metadata traversal.
func (m *Manager) preflightContext(ctx context.Context, o *Outcome) error {
	if err := scanner.Checkpoint(ctx); err != nil {
		return err
	}
	if !scanProfileSupported(o.Job.CapturedProfile) || o.Job.CapturedProfile.ID != o.Job.ProfileID || o.Job.CapturedProfile.Revision != o.Job.ProfileRevision {
		return errors.New("PROFILE_INVALID")
	}
	if o.ScopeManifest == nil || len(o.ScopeManifest.Roots) == 0 {
		return errors.New("SCOPE_MANIFEST_INVALID")
	}
	if err := m.reserve(); err != nil {
		return err
	}
	for _, r := range o.ScopeManifest.Roots {
		if err := scanner.Checkpoint(ctx); err != nil {
			return err
		}
		// Unavailable captured roots remain explicit scanner coverage gaps, not global scan failures.
		if r.Binding == nil {
			continue
		}
		b := r.Binding
		_, err := scanner.InspectBoundPath(scanner.RootBinding{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}, ".")
		if err != nil {
			code := "SOURCE_UNVERIFIED"
			if errors.Is(err, fs.ErrNotExist) {
				code = "SOURCE_MISSING"
			} else if errors.Is(err, fs.ErrPermission) {
				code = "SOURCE_UNREADABLE"
			}
			o.Job.Warnings = append(o.Job.Warnings, contracts.APIError{Code: code, MessageKey: "scan.preflight_source", RequestID: o.Job.ID})
		}
	}
	return nil
}

// executionFailure exposes stable reasons, never paths or driver diagnostics.
func executionFailure(err error) string {
	if errors.Is(err, context.Canceled) {
		return "SCAN_CANCELLED"
	}
	var sqliteError *sqlite.Error
	if errors.Is(err, syscall.ENOSPC) || (errors.As(err, &sqliteError) && sqliteError.Code()&255 == 13) {
		return "INDEX_SPACE_EXHAUSTED"
	}
	// Persist only fixed operation identifiers and numeric SQLite codes, never driver messages.
	var failure *scanner.Failure
	if errors.As(err, &failure) {
		code := failure.Error()
		if errors.Is(err, index.ErrGeneration) {
			return code + "_INVALID_GENERATION"
		}
		if errors.As(err, &sqliteError) {
			return code + "_SQLITE_" + strconv.Itoa(sqliteError.Code())
		}
		return code
	}
	for _, code := range []string{"INDEX_SPACE_LOW", "INDEX_SPACE_UNAVAILABLE", "PROFILE_INVALID", "SCOPE_MANIFEST_INVALID"} {
		if err != nil && err.Error() == code {
			return code
		}
	}
	return "SCAN_FAILED"
}

// Health checks initialized control state and real state-volume reserve without scanning sources.
func (m *Manager) Health(parent context.Context) (bool, string) {
	// Filesystem reserve inspection never owns the lifecycle mutex; a slow syscall cannot block status readers.
	ctx, cancel := m.observationContext(parent)
	defer cancel()
	if ctx.Err() != nil || m.readDB.PingContext(ctx) != nil {
		return false, "SERVICE_UNAVAILABLE"
	}
	if err := m.reserve(); err != nil {
		return false, executionFailure(err)
	}
	if ctx.Err() != nil {
		return false, "SERVICE_UNAVAILABLE"
	}
	return true, ""
}
