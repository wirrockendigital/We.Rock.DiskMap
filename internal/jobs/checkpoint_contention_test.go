package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Unrelated maintenance must not serialize every metadata entry, while cancellation remains immediate.
func TestRunningCheckpointDuringMaintenance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &Manager{ctx: ctx}
	runner := &jobRunner{id: "scan", ctx: ctx, desired: "run"}
	o := Outcome{}
	o.Job.ID = "scan"
	m.mu.Lock()
	defer m.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 10000; i++ {
			if err := m.runningCheckpoint(&o, runner); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("metadata checkpoints waited on unrelated maintenance")
	}
	cancel()
	if err := m.runningCheckpoint(&o, runner); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled worker continued", err)
	}
}
