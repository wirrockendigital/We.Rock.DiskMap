// Command diskmapctl waits for a scan performed by the existing package daemon.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// main never starts the service or changes its scanner identity.
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

// run keeps process exits separate from protocol parsing for real socket tests.
func run(args []string, in io.Reader, out, diagnostic io.Writer) int {
	flags := flag.NewFlagSet("diskmapctl", flag.ContinueOnError)
	// Parser errors may echo private argument values; only fixed diagnostics are emitted.
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "absolute state/run/control.sock path")
	version := flags.Int("protocol-version", 1, "control protocol version")
	exitVersion := flags.Int("exit-contract-version", ExitContractVersion, "CLI exit contract version")
	if flags.Parse(args) != nil {
		fmt.Fprintln(diagnostic, "INVALID_ARGUMENTS: invalid command arguments")
		return 2
	}
	if *version != 1 || *exitVersion != ExitContractVersion || flags.NArg() < 1 {
		return 2
	}
	// This static contract query needs no running service and exposes no runtime data.
	if flags.Arg(0) == "exit-contract" {
		if flags.NArg() != 1 {
			return 2
		}
		if json.NewEncoder(out).Encode(map[string]any{"exitContractVersion": ExitContractVersion, "codes": map[string]int{"complete": 0, "invalid": 2, "denied": 3, "unavailable": 4, "conflict": 5, "failedOrInterrupted": 6, "partial": 7, "deduplicatedComplete": 8, "cancelled": 9}}) != nil {
			return 4
		}
		return 0
	}
	if *socket == "" || (*socket)[0] != '/' {
		return 2
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
	}}}
	defer client.CloseIdleConnections()
	command := flags.Arg(0)
	switch command {
	case "diagnostic-preview", "diagnostic-download":
		return diagnosticExportCommand(client, command, flags.Args()[1:], in, out, diagnostic)
	case "health", "inventory", "file-type-mapping":
		if flags.NArg() != 1 {
			return 2
		}
		raw, code := request(client, diagnostic, "GET", "/api/v1/"+command, nil)
		if code != 0 {
			return code
		}
		_, err := out.Write(raw)
		if err != nil {
			return 4
		}
		// HTTP availability alone is not readiness; lifecycle callers rely on this exit code.
		if command == "health" && !healthReady(raw) {
			fmt.Fprintln(diagnostic, "SERVICE_UNAVAILABLE: service is not ready")
			return 4
		}
		return 0
	case "file-type-mapping-set":
		if flags.NArg() != 1 {
			return 2
		}
		raw, err := io.ReadAll(io.LimitReader(in, 32769))
		if err != nil || len(raw) > 32768 {
			return 2
		}
		raw, code := request(client, diagnostic, "PUT", "/api/v1/file-type-mapping", raw)
		if code != 0 {
			return code
		}
		if _, err = out.Write(raw); err != nil {
			return 4
		}
		return 0
	case "profile-import", "profile-rename", "profile-update", "preview-exclusions":
		if flags.NArg() != 1 {
			return 2
		}
		raw, err := io.ReadAll(io.LimitReader(in, (1<<20)+1))
		if err != nil || len(raw) > 1<<20 {
			return 2
		}
		// Private bootstrap operations use bounded stdin, never profile data in process arguments.
		endpoint := "/api/v1/profiles/import"
		if command == "profile-rename" {
			endpoint = "/api/v1/profiles/rename"
		}
		if command == "profile-update" {
			endpoint = "/api/v1/profiles/update"
		}
		if command == "preview-exclusions" {
			endpoint = "/api/v1/profiles/preview-exclusions"
		}
		raw, code := request(client, diagnostic, "POST", endpoint, raw)
		if code != 0 {
			return code
		}
		_, err = out.Write(raw)
		if err != nil {
			return 4
		}
		return 0
	case "scan":
		scan := flag.NewFlagSet("scan", flag.ContinueOnError)
		// Keep invalid scan flags out of diagnostic output as well.
		scan.SetOutput(io.Discard)
		id := scan.String("profile-id", "", "stored profile ID")
		mode := scan.String("mode", "full", "full, rebuild, or reconcile; each traverses the complete scope")
		if scan.Parse(flags.Args()[1:]) != nil {
			fmt.Fprintln(diagnostic, "INVALID_ARGUMENTS: invalid command arguments")
			return 2
		}
		if scan.NArg() != 0 {
			fmt.Fprintln(diagnostic, "INVALID_ARGUMENTS: unexpected scan arguments; copy the current task template")
			return 2
		}
		if !security.ValidID(*id) {
			fmt.Fprintln(diagnostic, "INVALID_PROFILE_ID: a valid stable profile ID is required; copy the current task template")
			return 2
		}
		if !contracts.ValidScanMode(*mode) {
			fmt.Fprintln(diagnostic, "INVALID_SCAN_MODE: use full, rebuild or reconcile; copy the current task template")
			return 2
		}
		payload, _ := json.Marshal(map[string]any{"contractVersion": 1, "profileId": *id, "mode": *mode})
		raw, code := request(client, diagnostic, "POST", "/api/v1/jobs", payload)
		if code != 0 {
			return code
		}
		var accepted contracts.Envelope[jobs.Submission]
		if json.Unmarshal(raw, &accepted) != nil || accepted.ValidateVersion() != nil || !security.ValidID(accepted.Data.Job.ID) || accepted.Data.Job.ProfileID != *id || accepted.Data.Job.ScanMode != *mode {
			return 4
		}
		consecutiveFailures := 0
		for {
			// Only read-only polling retries transient availability failures; acceptance is never resubmitted.
			var pollDiagnostic bytes.Buffer
			raw, code = request(client, &pollDiagnostic, "GET", "/api/v1/jobs/"+accepted.Data.Job.ID+"/wait-status", nil)
			if code == 4 && pollDiagnostic.Len() > 0 {
				consecutiveFailures++
				if consecutiveFailures < 3 {
					time.Sleep(500 * time.Millisecond)
					continue
				}
				_, _ = diagnostic.Write(pollDiagnostic.Bytes())
				return 4
			}
			if code != 0 {
				return code
			}
			var envelope contracts.Envelope[jobs.Outcome]
			if json.Unmarshal(raw, &envelope) != nil || envelope.ValidateVersion() != nil || envelope.Data.Job.ProfileID != *id || envelope.Data.Job.ID != accepted.Data.Job.ID || envelope.Data.Job.ScanMode != *mode {
				return 4
			}
			consecutiveFailures = 0
			var waiting bool
			code, waiting = terminalExit(envelope.Data, accepted.Data.Deduplicated)
			if waiting {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if code == 4 {
				return code
			}
			// Include join provenance in terminal output without changing the canonical job.
			result := contracts.Envelope[jobs.Submission]{ContractVersion: 1, RequestID: envelope.RequestID, Data: jobs.Submission{Outcome: envelope.Data, Deduplicated: accepted.Data.Deduplicated}}
			if json.NewEncoder(out).Encode(cliResult{Envelope: result, ExitContractVersion: ExitContractVersion}) != nil {
				return 4
			}
			return code
		}
	default:
		fmt.Fprintln(diagnostic, "commands: exit-contract, health, inventory, profile-import, profile-rename, profile-update, preview-exclusions, scan --profile-id=ID --mode=full|rebuild|reconcile")
		return 2
	}
}

// request bounds responses and maps transport or authorization failures distinctly.
func request(client *http.Client, diagnostic io.Writer, method, path string, payload []byte) ([]byte, int) {
	req, err := http.NewRequest(method, "http://unix"+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 2
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		// Permission denial remains distinct through net.OpError and url.Error wrapping.
		if errors.Is(err, os.ErrPermission) {
			return nil, 3
		}
		// Report only the transport category; socket paths and wrapped errors remain private.
		fmt.Fprintln(diagnostic, "SERVICE_UNAVAILABLE: service unavailable for this request")
		return nil, 4
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		// Interrupted response transport is unavailable, while a size violation is a protocol failure.
		fmt.Fprintln(diagnostic, "SERVICE_UNAVAILABLE: service unavailable for this request")
		return nil, 4
	}
	if len(raw) > 1<<20 {
		return nil, 4
	}
	switch response.StatusCode {
	case 200:
		return raw, 0
	case 400, 404:
		// Only verified fixed codes select a diagnosis; backend prose and identifiers are never echoed.
		fmt.Fprintln(diagnostic, rejectionDiagnostic(method, path, response.StatusCode, raw))
		return nil, 2
	case 401, 403:
		return nil, 3
	case 409:
		return nil, 5
	case 503:
		// Availability rejection is distinct from malformed protocol data with the same exit code.
		fmt.Fprintln(diagnostic, "SERVICE_UNAVAILABLE: service unavailable for this request")
		return nil, 4
	default:
		return nil, 6
	}
}

// rejectionDiagnostic distinguishes supported error evidence from an empty or malformed response.
func rejectionDiagnostic(method, path string, status int, raw []byte) string {
	const generic = "REQUEST_REJECTED: request rejected; check the active profile and copy its current task template"
	if method != "POST" || path != "/api/v1/jobs" {
		return generic
	}
	var envelope struct {
		ContractVersion int                `json:"contractVersion"`
		Error           contracts.APIError `json:"error"`
	}
	if security.DecodeJSON(bytes.NewReader(raw), 4096, &envelope) != nil || envelope.ContractVersion != 1 {
		return generic
	}
	if status == 404 && envelope.Error.Code == "PROFILE_NOT_FOUND" {
		return "PROFILE_NOT_FOUND: profile not found; select an existing active profile and copy its current task template"
	}
	if status == 400 && envelope.Error.Code == "INVALID_REQUEST" {
		return "SCAN_PROFILE_REJECTED: profile is missing, inactive or not valid for scanning; check it in the app and copy the current task template"
	}
	return generic
}
