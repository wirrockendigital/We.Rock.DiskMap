package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
)

// TestDiagnosticsRetiresOnlyOwnedRegularSink preserves outside content behind aliases.
func TestDiagnosticsRetiresOnlyOwnedRegularSink(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "alias"}[alias], func(t *testing.T) {
			state := t.TempDir()
			if err := os.Chmod(state, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(state, "logs"), 0700); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(state, "service.log")
			if alias {
				outside := filepath.Join(t.TempDir(), "private")
				if err := os.WriteFile(outside, []byte("sentinel"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(name, []byte("old diagnostic"), 0600); err != nil {
				t.Fatal(err)
			}
			logger, err := openDiagnostics(state)
			if alias {
				if err == nil {
					logger.Close()
					t.Fatal("alias accepted")
				}
				got, _ := os.ReadFile(name)
				if string(got) != "sentinel" {
					t.Fatal("outside content altered")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			info, err := os.Stat(name)
			if err != nil || info.Size() != 0 {
				t.Fatal("old sink not bounded", err)
			}
			if err = logger.Record(diagnosticEvent("SERVICE_READY", "idle")); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(state, "logs", "slot-0.jsonl"))
			if err != nil || !strings.Contains(string(raw), `"code":"SERVICE_READY"`) {
				t.Fatal("event absent", err)
			}
		})
	}
}

// TestDiagnosticsObserverStopsWithoutSampling avoids status reads after service cancellation.
func TestDiagnosticsObserverStopsWithoutSampling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := observeDiagnostics(ctx, nil, func(context.Context) diagnostics.Event {
		t.Fatal("sample after cancellation")
		return diagnostics.Event{}
	}); err != nil {
		t.Fatal(err)
	}
}
