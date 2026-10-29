package jobs

import (
	"context"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// StorageStatus reports observed package metadata, not a prediction of the next scan's peak space.
type StorageStatus struct {
	UnsupportedGenerations int                  `json:"unsupportedGenerations"`
	PendingQualification   int                  `json:"pendingQualification"`
	CompleteCount          int                  `json:"completeCount"`
	DataPath               string               `json:"dataPath"`
	IndexBytes             *contracts.Bytes     `json:"indexBytes"`
	TemporaryBytes         *contracts.Bytes     `json:"temporaryBytes"`
	JournalBytes           *contracts.Bytes     `json:"journalBytes"`
	FreeBytes              *contracts.Bytes     `json:"freeBytes"`
	ReserveBytes           contracts.Bytes      `json:"reserveBytes"`
	TemporaryRequiredBytes *contracts.Bytes     `json:"temporaryRequiredBytes"`
	MeasurementState       string               `json:"measurementState"`
	MeasuredAt             string               `json:"measuredAt"`
	RetainedSnapshots      []retainedGeneration `json:"retainedSnapshots"`
	RetainedTruncated      bool                 `json:"retainedTruncated"`
	PendingCleanup         int                  `json:"pendingCleanup"`
}

// Storage reads are bounded and cannot trigger cleanup or a source scan.
func (m *Manager) Storage(ctx context.Context, profile string) (StorageStatus, error) {
	// Maintenance lock contention must not consume the metadata work budget.
	// The parent deadline still bounds the request and is never extended.
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	out := StorageStatus{DataPath: m.state, ReserveBytes: contracts.Bytes("67108864"), MeasurementState: "unavailable", MeasuredAt: now(), RetainedSnapshots: []retainedGeneration{}}
	if m.closed {
		return out, ErrUnavailable
	}
	if !identifier.MatchString(profile) {
		return out, ErrInvalid
	}
	if err := m.db.QueryRowContext(ctx, `SELECT complete_count FROM retention_settings WHERE singleton=1`).Scan(&out.CompleteCount); err != nil {
		return out, err
	}
	if err := m.db.QueryRowContext(ctx, `SELECT count(*) FROM retention_cleanup`).Scan(&out.PendingCleanup); err != nil {
		return out, err
	}
	_ = m.db.QueryRowContext(ctx, `SELECT count(*) FROM retention_qualification WHERE stamp=''`).Scan(&out.PendingQualification)
	_ = m.db.QueryRowContext(ctx, `SELECT count(*) FROM retention_qualification WHERE stamp='unsupported'`).Scan(&out.UnsupportedGenerations)
	if n, err := m.availableStateBytes(); err == nil {
		v := contracts.Bytes(new(big.Int).SetUint64(n).String())
		out.FreeBytes = &v
	}
	sums, err := measureStorage(ctx, m.state)
	if err == nil {
		out.MeasurementState = "complete"
		values := []**contracts.Bytes{&out.IndexBytes, &out.TemporaryBytes, &out.JournalBytes}
		for i, p := range values {
			v := contracts.Bytes(sums[i].String())
			*p = &v
		}
	} else {
		out.MeasurementState = "partial"
	}
	snapshots, e := m.qualifiedCompletes(ctx, profile)
	if e == nil {
		if len(snapshots) > 31 {
			out.RetainedTruncated = true
			snapshots = snapshots[:31]
		}
		out.RetainedSnapshots = snapshots
	} else {
		out.RetainedTruncated = true
	}
	if out.FreeBytes == nil {
		out.MeasurementState = "partial"
	}
	return out, nil
}

// measureStorage walks at most 4096 own metadata entries without following symlinked directories.
func measureStorage(ctx context.Context, state string) ([3]*big.Int, error) {
	sums := [3]*big.Int{new(big.Int), new(big.Int), new(big.Int)}
	type entry struct {
		path  string
		depth int
	}
	queue := []entry{{".", 0}}
	seen := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return sums, err
		}
		x := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		root, _, err := scanner.OpenBoundRoot(filepath.Clean(filepath.Join(state, x.path)))
		if err != nil {
			return sums, err
		}
		f, err := root.Open(".")
		if err != nil {
			root.Close()
			return sums, err
		}
		for {
			items, e := f.ReadDir(64)
			if e != nil && !errors.Is(e, io.EOF) {
				f.Close()
				root.Close()
				return sums, e
			}
			for _, item := range items {
				seen++
				if seen > 4096 || ctx.Err() != nil {
					f.Close()
					root.Close()
					return sums, ErrActionBudget
				}
				info, e := root.Lstat(item.Name())
				if e != nil {
					f.Close()
					root.Close()
					return sums, e
				}
				relative := filepath.Join(x.path, item.Name())
				if info.Mode()&os.ModeSymlink != 0 {
					f.Close()
					root.Close()
					return sums, ErrUnavailable
				}
				if info.IsDir() {
					if x.depth >= 4 {
						f.Close()
						root.Close()
						return sums, ErrActionBudget
					}
					queue = append(queue, entry{relative, x.depth + 1})
					continue
				}
				if !info.Mode().IsRegular() {
					continue
				}
				category := 0
				if strings.HasSuffix(item.Name(), "-journal") || strings.HasSuffix(item.Name(), "-wal") || strings.HasSuffix(item.Name(), "-shm") {
					category = 2
				} else if strings.Contains(relative, ".staging/") {
					category = 1
				}
				sums[category].Add(sums[category], big.NewInt(info.Size()))
			}
			if errors.Is(e, io.EOF) {
				break
			}
		}
		f.Close()
		root.Close()
	}
	return sums, nil
}
