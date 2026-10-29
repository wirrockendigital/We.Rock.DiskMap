// Package service hosts the local control API without exposing a network listener.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ErrPackageStore identifies startup failures without exposing database paths.
var ErrPackageStore = errors.New("PACKAGE_STORE_UNAVAILABLE")

// ErrPackageDiagnostics identifies startup diagnostics failures without exposing private paths.
var ErrPackageDiagnostics = errors.New("PACKAGE_DIAGNOSTICS_UNAVAILABLE")

// Run binds a fresh owner-only Unix socket and shuts down when its context ends.
func Run(ctx context.Context, socket string) (runErr error) {
	// Validate the state boundary before creating a socket or opening any database.
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || filepath.Base(socket) != "control.sock" || filepath.Base(filepath.Dir(socket)) != "run" {
		return errors.New("INVALID_RUNTIME_SOCKET")
	}
	if err := security.RuntimeIdentity(os.Geteuid(), os.Getegid()); err != nil {
		return err
	}
	if err := security.PrepareRuntime(filepath.Dir(filepath.Dir(socket))); err != nil {
		return err
	}
	// A child context joins the observer and interrupts jobs on a diagnostic storage failure.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	return RunListener(ctx, filepath.Dir(filepath.Dir(socket)), listener, uint32(os.Geteuid()))
}

// RunListener serves only the package account and root CLI peers; scanning never changes identity.
func RunListener(ctx context.Context, state string, listener net.Listener, packageUID uint32) (runErr error) {
	if security.RuntimeIdentity(os.Geteuid(), os.Getegid()) != nil || packageUID != uint32(os.Geteuid()) {
		return errors.New("INVALID_PACKAGE_IDENTITY")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	socket := filepath.Join(state, "run", "control.sock")
	manager, err := jobs.Open(ctx, filepath.Dir(filepath.Dir(socket)))
	if err != nil {
		return errors.Join(ErrPackageStore, err)
	}
	// The manager lock establishes a single writer before any diagnostic file mutation.
	logger, err := openDiagnostics(filepath.Dir(filepath.Dir(socket)))
	if err != nil {
		return errors.Join(ErrPackageDiagnostics, err, manager.Close())
	}
	defer logger.Close()
	if err = logger.Record(diagnosticEvent("SERVICE_STARTING", "idle")); err != nil {
		return errors.Join(err, manager.Close())
	}
	defer func() {
		code, phase := "SERVICE_STOPPED", "idle"
		if runErr != nil {
			code, phase = "SERVICE_FAILED", "unavailable"
		}
		runErr = errors.Join(runErr, logger.Record(diagnosticEvent(code, phase)))
	}()
	defer func() { runErr = errors.Join(runErr, manager.Close()) }()
	mux := http.NewServeMux()
	registerJobs(mux, manager)
	// Global observers read one bounded, transactionally consistent queue snapshot.
	registerStatusSnapshot(mux, manager)
	registerRegionalContext(mux)
	// CLI waiters read committed job state without opening a stored index.
	registerWaitStatus(mux, manager)
	registerStorage(mux, manager)
	registerPreferences(mux, manager)
	// Export reads one consistent configuration snapshot without index or source data.
	registerConfigurationExport(mux, manager)
	// Import applies explicit mappings atomically; diagnostics freeze the reviewed projection.
	registerConfigurationImport(mux, manager)
	registerDiagnosticExport(mux, manager)
	// Shared templates remain separate from frozen profile rules.
	registerExclusionTemplate(mux, manager)
	registerOnboarding(mux, manager)
	// Profile cards read one bounded control-metadata snapshot.
	registerProfileSummaries(mux, manager)
	// Restored state and navigation use the same principal-bound generation leases.
	registerViewRestore(mux, manager)
	registerDiagnostics(mux, manager)
	// Progress and history observe existing jobs without starting filesystem work.
	registerProgress(mux, manager)
	registerScanPreview(mux, manager)
	// Integrity actions are explicit, replay-bound operations on owned index data.
	registerIntegrity(mux, manager)
	registerHierarchy(mux, manager)
	registerSearch(mux, manager)
	// Metric availability and capacity provenance share the existing immutable view boundary.
	registerSizeMetrics(mux, manager)
	// Historical path metadata stays bound to the existing reader lease.
	registerLeasedPaths(mux, manager)
	// Historical detail reads share the existing principal-bound reader reservation.
	registerNodeDetails(mux, manager)
	registerSchedulerTemplate(mux, manager)
	registerSchedulerStatus(mux, manager)
	registerExclusions(mux, manager)
	registerViews(mux, manager, "/etc/VERSION", "/etc/samba/smb.share.conf")
	registerInventory(mux, filepath.Dir(filepath.Dir(socket)))
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		ready, reason := manager.Health(r.Context())
		status := "ready"
		if !ready {
			status = "unavailable"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": 1, "requestId": "health", "data": map[string]any{"status": status, "ready": ready, "reasonCode": reason}})
	})
	// Query diagnostics use bounded asynchronous writes behind the existing peer authorization gate.
	traced := traceQueries(mux, queryTimingSink(ctx, logger.Record))
	// File permissions and kernel peer credentials jointly protect the local API.
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, _ := r.Context().Value(peerKey{}).(bool)
		if !allowed {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		traced.ServeHTTP(w, r)
	})
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return ctx }, Handler: protected, ConnContext: peerContextFor(packageUID), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	if err = logger.Record(diagnosticEvent("SERVICE_READY", "idle")); err != nil {
		return err
	}
	observed := make(chan error, 1)
	go func() {
		err := observeDiagnostics(ctx, logger, func(ctx context.Context) diagnostics.Event {
			status, err := manager.DiagnosticStatus(ctx)
			if err != nil {
				return diagnosticEvent("SERVICE_STATUS", "unavailable")
			}
			phase := status.Phase
			if phase == "unknown" {
				phase = "unavailable"
			}
			event := diagnosticEvent("SERVICE_STATUS", phase)
			if status.ElapsedMillis != nil {
				event.ElapsedMillis = *status.ElapsedMillis
			}
			if status.Warnings != nil {
				event.Warnings = *status.Warnings
			}
			return event
		})
		observed <- err
		if err != nil {
			cancel()
		}
	}()
	serveErr := serveUntilCanceled(ctx, server, listener, 5*time.Second)
	cancel()
	return errors.Join(serveErr, <-observed)
}

// serveUntilCanceled joins graceful shutdown before allowing the daemon to exit.
func serveUntilCanceled(ctx context.Context, server *http.Server, listener net.Listener, grace time.Duration) error {
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		shutdownErr := server.Shutdown(shutdown)
		if shutdownErr != nil {
			// Expired grace must close active sockets rather than leave a live server behind.
			closeErr := server.Close()
			if closeErr != nil {
				shutdownErr = errors.Join(shutdownErr, closeErr)
			}
		}
		serveErr := <-served
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		return errors.Join(shutdownErr, serveErr)
	}
}
