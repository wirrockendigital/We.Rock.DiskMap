// Command diskmapd runs the package-owned local service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/uninstall"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Main requires an explicit runtime socket so a development invocation cannot target DSM implicitly.
func main() {
	socket := flag.String("socket", "", "absolute path to the package control socket")
	upgrade := flag.String("upgrade", "", "offline prepare, apply, or explicit restore")
	remove := flag.Bool("uninstall-data", false, "remove verified package-owned data offline")
	check := flag.Bool("check-runtime", false, "validate private package runtime without starting the service")
	flag.Parse()
	if *socket == "" || (*socket)[0] != '/' {
		fmt.Fprintln(os.Stderr, "an absolute --socket is required")
		os.Exit(2)
	}
	// Refuse root before runtime file creation, including lifecycle preflight.
	if err := security.RuntimeIdentity(os.Geteuid(), os.Getegid()); err != nil {
		fmt.Fprintln(os.Stderr, "service identity rejected")
		os.Exit(1)
	}
	if filepath.Clean(*socket) != *socket || filepath.Base(*socket) != "control.sock" || filepath.Base(filepath.Dir(*socket)) != "run" {
		os.Exit(2)
	}
	// Uninstall must validate existing objects without creating or chmodding runtime paths first.
	if *remove {
		if *upgrade != "" || *check || flag.NArg() != 0 {
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := uninstall.Remove(ctx, filepath.Dir(filepath.Dir(*socket))); err != nil {
			fmt.Fprintln(os.Stderr, "uninstall data removal incomplete")
			os.Exit(1)
		}
		return
	}
	if err := security.PrepareRuntime(filepath.Dir(filepath.Dir(*socket))); err != nil {
		fmt.Fprintln(os.Stderr, "runtime rejected")
		os.Exit(1)
	}
	// Upgrade hooks use the new installed binary before ordinary service startup.
	if *upgrade != "" {
		if *check || flag.NArg() != 0 {
			os.Exit(2)
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code := runOfflineUpgrade(ctx, filepath.Dir(filepath.Dir(*socket)), *upgrade, os.Stderr, jobs.Upgrade)
		cancel()
		if code != 0 {
			os.Exit(code)
		}
		return
	}
	if *check {
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// The restricted package daemon owns the shared administrator scan store.
	if err := service.Run(ctx, *socket); err != nil {
		// Runtime diagnostics must not expose source names, paths, or credential-bearing errors.
		fmt.Fprintln(os.Stderr, "service failed")
		os.Exit(1)
	}
}

// Offline diagnostics use only closed mode/outcome codes, never the underlying error text.
func runOfflineUpgrade(ctx context.Context, state, mode string, output io.Writer, run func(context.Context, string, string) error) int {
	labels := map[string]string{"prepare": "PREPARE", "apply": "APPLY", "restore": "RESTORE"}
	label, valid := labels[mode]
	if !valid {
		fmt.Fprintln(output, "UPGRADE_INVALID_MODE")
		return 1
	}
	fmt.Fprintln(output, "UPGRADE_"+label+"_START")
	err := run(ctx, state, mode)
	code := "SUCCESS"
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = "DEADLINE_EXCEEDED"
		case errors.Is(err, context.Canceled):
			code = "CANCELLED"
		default:
			code = "FAILED"
		}
	}
	fmt.Fprintln(output, "UPGRADE_"+label+"_"+code)
	if err != nil {
		return 1
	}
	return 0
}
