package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Every offline mode emits only the closed code set even when failures contain private text.
func TestOfflineUpgradeDiagnostics(t *testing.T) {
	for _, mode := range []string{"prepare", "apply", "restore"} {
		for _, tc := range []struct {
			err     error
			outcome string
			exit    int
		}{
			{nil, "SUCCESS", 0},
			{fmt.Errorf("private/path secret: %w", context.DeadlineExceeded), "DEADLINE_EXCEEDED", 1},
			{fmt.Errorf("private/path secret: %w", context.Canceled), "CANCELLED", 1},
			{errors.New("private/path secret"), "FAILED", 1},
		} {
			t.Run(mode+tc.outcome, func(t *testing.T) {
				var out bytes.Buffer
				ctx := context.WithValue(context.Background(), struct{}{}, "bound")
				code := runOfflineUpgrade(ctx, "private-state", mode, &out, func(got context.Context, state, m string) error {
					if got != ctx || state != "private-state" || m != mode {
						t.Fatal("arguments changed")
					}
					return tc.err
				})
				want := "UPGRADE_" + strings.ToUpper(mode) + "_START\nUPGRADE_" + strings.ToUpper(mode) + "_" + tc.outcome + "\n"
				if code != tc.exit || out.String() != want {
					t.Fatalf("unexpected code/output: %d %q", code, out.String())
				}
			})
		}
	}
	var out bytes.Buffer
	if code := runOfflineUpgrade(context.Background(), "private", "private-invalid", &out, func(context.Context, string, string) error { t.Fatal("invalid mode executed"); return nil }); code != 1 || out.String() != "UPGRADE_INVALID_MODE\n" {
		t.Fatal("invalid mode contract")
	}
}
