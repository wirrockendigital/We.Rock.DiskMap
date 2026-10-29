package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Preset values are a stable qualified contract, not configurable upper-bound suggestions.
func TestPresetPolicies(t *testing.T) {
	for _, want := range []Policy{
		{1, "gentle", 1, 32, 32, 8 * time.Millisecond, 2048, 1024},
		{1, "balanced", 1, 64, 64, 2 * time.Millisecond, 4096, 2048},
		{1, "fast", 1, 128, 128, 0, 8192, 2048},
	} {
		got, err := Resolve(want.Mode, contracts.ResourceLimits{Workers: 1})
		if err != nil || got != want || got.Validate() != nil {
			t.Fatalf("%+v %v", got, err)
		}
		if got.Workers > MaxWorkers || got.DirectoryBatch > MaxDirectoryBatch || got.SQLiteCacheKiB > MaxSQLiteCacheKiB || got.TempCacheKiB > MaxTempCacheKiB {
			t.Fatal("ceiling exceeded")
		}
	}
}

// Invalid and unsupported fields must fail explicitly, including optional numeric zero.
func TestUnsupportedPolicies(t *testing.T) {
	for _, mode := range []string{"", "Gentle", "custom", "turbo"} {
		if _, err := Resolve(mode, contracts.ResourceLimits{Workers: 1}); !errors.Is(err, ErrUnsupported) {
			t.Fatal(mode, err)
		}
	}
	zero := 0
	bytes := contracts.Bytes("0")
	for _, limits := range []contracts.ResourceLimits{{Workers: 0}, {Workers: -1}, {Workers: 2}, {Workers: 1 << 30}, {Workers: 1, CPUPercent: &zero}, {Workers: 1, IOBytesPerSecond: &bytes}} {
		if _, err := Resolve("gentle", limits); !errors.Is(err, ErrUnsupported) {
			t.Fatal(limits, err)
		}
	}
	p, _ := Resolve("gentle", contracts.ResourceLimits{Workers: 1})
	for _, mutate := range []func(*Policy){func(p *Policy) { p.Workers = 2 }, func(p *Policy) { p.DirectoryBatch = 129 }, func(p *Policy) { p.PaceEvery = 0 }, func(p *Policy) { p.PaceDelay = -1 }, func(p *Policy) { p.SQLiteCacheKiB = 8193 }, func(p *Policy) { p.TempCacheKiB = 2049 }, func(p *Policy) { p.Version = 2 }} {
		bad := p
		mutate(&bad)
		if _, err := NewPacer(bad); !errors.Is(err, ErrUnsupported) {
			t.Fatal(bad, err)
		}
	}
}

// An injected wait records the exact work boundary without timing-sensitive rate assertions.
func TestPacerWorkAndCapturedPolicy(t *testing.T) {
	for _, mode := range []string{"gentle", "balanced", "fast"} {
		policy, _ := Resolve(mode, contracts.ResourceLimits{Workers: 1})
		p, err := NewPacer(policy)
		if err != nil {
			t.Fatal(err)
		}
		original := policy
		policy.PaceEvery = 1
		waits := 0
		p.wait = func(ctx context.Context, d time.Duration) error {
			waits++
			if d != original.PaceDelay {
				t.Fatal(d)
			}
			return nil
		}
		for i := 0; i < original.PaceEvery*3+original.PaceEvery-1; i++ {
			if err := p.Step(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		want := 3
		if mode == "fast" {
			want = 0
		}
		if waits != want {
			t.Fatalf("%s waits=%d", mode, waits)
		}
	}
}

// Cancellation is checked even for fast mode and interrupts a real timer wait.
func TestPacerCancellation(t *testing.T) {
	for _, mode := range []string{"gentle", "fast"} {
		policy, _ := Resolve(mode, contracts.ResourceLimits{Workers: 1})
		p, _ := NewPacer(policy)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if !errors.Is(p.Step(ctx), context.Canceled) || p.pending != 0 {
			t.Fatal("cancelled work counted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(started); done <- waitContext(ctx, time.Hour) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait did not cancel")
	}
	if err := waitContext(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
