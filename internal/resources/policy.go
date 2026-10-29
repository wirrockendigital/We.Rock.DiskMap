// Package resources defines the qualified per-scan resource policy and cooperative pacing.
package resources

import (
	"errors"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// ErrUnsupported rejects settings whose limits cannot currently be enforced.
var ErrUnsupported = errors.New("RESOURCE_POLICY_UNSUPPORTED")

// Hard ceilings cannot be overridden through profile settings or preset names.
const (
	PolicyVersion     = 1
	MaxWorkers        = 1
	MaxDirectoryBatch = 128
	MaxSQLiteCacheKiB = 8192
	MaxTempCacheKiB   = 2048
)

// Policy is a detached value captured for one scan, not a process-wide RSS guarantee.
// SQLite budgets apply to one connection; pacing controls activity, not physical byte throughput.
type Policy struct {
	Version        int
	Mode           string
	Workers        int
	DirectoryBatch int
	PaceEvery      int
	PaceDelay      time.Duration `json:"-"`
	SQLiteCacheKiB int
	TempCacheKiB   int
}

// Resolve admits only qualified presets and refuses even zero-valued unsupported optional limits.
func Resolve(mode string, limits contracts.ResourceLimits) (Policy, error) {
	if limits.Workers != MaxWorkers || limits.IOBytesPerSecond != nil || limits.CPUPercent != nil {
		return Policy{}, ErrUnsupported
	}
	p := Policy{Version: PolicyVersion, Mode: mode, Workers: MaxWorkers}
	switch mode {
	case "gentle":
		p.DirectoryBatch, p.PaceEvery, p.PaceDelay = 32, 32, 8*time.Millisecond
		p.SQLiteCacheKiB, p.TempCacheKiB = 2048, 1024
	case "balanced":
		p.DirectoryBatch, p.PaceEvery, p.PaceDelay = 64, 64, 2*time.Millisecond
		p.SQLiteCacheKiB, p.TempCacheKiB = 4096, 2048
	case "fast":
		p.DirectoryBatch, p.PaceEvery, p.PaceDelay = 128, 128, 0
		p.SQLiteCacheKiB, p.TempCacheKiB = 8192, 2048
	default:
		return Policy{}, ErrUnsupported
	}
	return p, nil
}

// Validate rejects forged, partially initialized or modified copies of a preset policy.
func (p Policy) Validate() error {
	expected, err := Resolve(p.Mode, contracts.ResourceLimits{Workers: MaxWorkers})
	if err != nil || p != expected {
		return ErrUnsupported
	}
	return nil
}
