package resources

import (
	"context"
	"time"
)

// Pacer belongs to one sequential scan worker; callers must not share it across goroutines.
type Pacer struct {
	policy  Policy
	pending int
	wait    func(context.Context, time.Duration) error
}

// NewPacer validates and copies a preset so later caller-side changes cannot alter a running scan.
func NewPacer(policy Policy) (*Pacer, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Pacer{policy: policy, wait: waitContext}, nil
}

// Step accounts for one processed metadata entry, yielding only at the preset interval.
// Call outside manager locks and after the chosen observation boundary, never for timer ticks.
func (p *Pacer) Step(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.pending++
	if p.pending < p.policy.PaceEvery {
		return nil
	}
	p.pending = 0
	if p.policy.PaceDelay == 0 {
		return ctx.Err()
	}
	return p.wait(ctx, p.policy.PaceDelay)
}

// waitContext permits cancellation while yielding without leaving timers or helper goroutines behind.
func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
