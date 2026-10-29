package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// openDiagnostics retires the old package-owned stderr sink before using bounded structured slots.
func openDiagnostics(state string) (*diagnostics.Logger, error) {
	f, err := security.OpenPrivateFile(filepath.Join(state, "service.log"), os.O_WRONLY)
	if err == nil {
		err = f.Truncate(0)
		if err == nil {
			err = f.Sync()
		}
		err = errors.Join(err, f.Close())
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, diagnostics.ErrUnavailable
	}
	return diagnostics.New(filepath.Join(state, "logs"))
}

// diagnosticEvent exposes only implementation versions and the fixed event vocabulary.
func diagnosticEvent(code, phase string) diagnostics.Event {
	scanner, _ := strconv.ParseUint(index.ScannerVersion, 10, 64)
	return diagnostics.Event{Code: code, Phase: phase, Schema: uint64(index.GenerationSchema), Scanner: scanner, Aggregate: uint64(index.AggregateVersion)}
}

// observeDiagnostics samples metadata periodically; logging never runs on the scanner hot path.
func observeDiagnostics(ctx context.Context, logger *diagnostics.Logger, sample func(context.Context) diagnostics.Event) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Each record also expires old slots, including during idle service periods.
			if err := logger.Record(sample(ctx)); err != nil {
				return err
			}
		}
	}
}
