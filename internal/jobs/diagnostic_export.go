package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"runtime"
	"sort"
	"time"

	diagnosticexport "github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics/export"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// DiagnosticExportInput projects real observations into the closed archive vocabulary.
// Version strings are supplied by the service's bounded package/DSM metadata reader.
// RetainedSnapshots stays unknown: recorded integrity rows do not prove current files.
func (m *Manager) DiagnosticExportInput(ctx context.Context, principal, appVersion, dsmVersion string) (diagnosticexport.Input, error) {
	var zero diagnosticexport.Input
	bundle, err := m.ConfigurationExport(ctx, principal)
	if err != nil {
		return zero, err
	}
	status, err := m.DiagnosticStatus(ctx)
	if err != nil {
		return zero, err
	}
	profiles, exclusions := uint64(len(bundle.Bundle.Profiles)), uint64(len(bundle.Bundle.Global.ExclusionTemplate.Rules))
	for _, p := range bundle.Bundle.Profiles {
		exclusions += uint64(len(p.Configuration.Exclusions))
	}
	in := diagnosticexport.Input{Version: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339), Versions: diagnosticexport.Versions{App: appVersion, DSM: dsmVersion, Architecture: runtime.GOARCH, Control: migration.ControlVersion, Generation: status.Versions.Schema, Scanner: status.Versions.Scanner}, Configuration: diagnosticexport.Configuration{Profiles: &profiles, ExclusionRules: &exclusions}, Components: []diagnosticexport.Component{}, Capabilities: []diagnosticexport.Capability{}, Faults: []diagnosticexport.Fault{}}
	phase := "unknown"
	switch status.Phase {
	case "idle":
		phase = "idle"
	case "paused":
		phase = "paused"
	case "queued", "preflight", "scanning", "aggregating", "validating", "publishing":
		phase = "running"
	}
	storage := status.Storage.State
	if storage == "low" {
		storage = "degraded"
	}
	in.Components = []diagnosticexport.Component{{Kind: "backend", State: status.Backend}, {Kind: "scanner", State: phase}, {Kind: "queue", State: status.Queue.State}, {Kind: "control-database", State: status.ControlDatabase.Availability}, {Kind: "generation-index", State: "unknown"}, {Kind: "storage", State: storage}, {Kind: "logger", State: "unknown"}, {Kind: "ui", State: "unknown"}}
	// Implementation availability is not a DSM qualification claim.
	for _, k := range []string{"local-api", "snapshot-reads", "job-control", "integrity-checks", "file-station", "scheduled-scan", "system-notifications"} {
		in.Capabilities = append(in.Capabilities, diagnosticexport.Capability{Kind: k, State: "unknown"})
	}
	scan := "unknown"
	if status.Capabilities.ScanAvailable != nil && !*status.Capabilities.ScanAvailable {
		scan = "unavailable"
	}
	in.Capabilities = append(in.Capabilities, diagnosticexport.Capability{Kind: "scan", State: scan})
	for _, code := range status.ReasonCodes {
		in.Faults = append(in.Faults, diagnosticexport.Fault{Code: code, Count: 1})
	}
	work, cancel := m.observationContext(ctx)
	defer cancel()
	paths, err := retryControlRead(work, func() ([]diagnosticexport.PrivatePath, error) { return m.diagnosticExportPaths(work) })
	if err != nil {
		return zero, observationError(err)
	}
	in.PrivatePaths = paths
	raw, err := json.Marshal(in)
	if err != nil {
		return zero, ErrUnavailable
	}
	if _, err = diagnosticexport.Decode(raw); err != nil {
		return zero, ErrUnavailable
	}
	return in, nil
}

// diagnosticExportPaths reads historical configured paths only; it never opens sources.
// Private fields remain server-side until the separate projection option is selected.
func (m *Manager) diagnosticExportPaths(ctx context.Context) ([]diagnosticexport.PrivatePath, error) {
	tx, e := m.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT CASE WHEN length(CAST(roots_json AS BLOB))<=65536 THEN roots_json END FROM profiles ORDER BY id LIMIT 201`)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	paths := []diagnosticexport.PrivatePath{{Kind: "package-state", Path: m.state}}
	count := 0
	for rows.Next() {
		count++
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return nil, e
		}
		if count > 200 {
			rows.Close()
			return nil, ErrUnavailable
		}
		var bindings map[string]scanner.RootBinding
		if security.DecodeJSON(bytes.NewBufferString(raw), 65536, &bindings) != nil {
			rows.Close()
			return nil, ErrUnavailable
		}
		for _, b := range bindings {
			if b.Path != "" && !seen[b.Path] {
				seen[b.Path] = true
				paths = append(paths, diagnosticexport.PrivatePath{Kind: "source-root", Path: b.Path})
			}
		}
		if len(paths) > diagnosticexport.MaxPrivatePaths {
			rows.Close()
			return nil, ErrUnavailable
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Kind != paths[j].Kind {
			return paths[i].Kind < paths[j].Kind
		}
		return paths[i].Path < paths[j].Path
	})
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return paths, nil
}
