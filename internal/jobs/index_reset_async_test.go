package jobs

import (
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"testing"
	"time"
)

// Polling must stay responsive while the mutation waits, and retries must retain one identity.
func TestIndexResetPollingDoesNotWaitForMutationLock(t *testing.T) {
	m, _, _ := bootFixture(t)
	request := IndexResetRequest{ContractVersion: 1, All: true, Confirmed: true}
	m.mu.Lock()
	start := time.Now()
	pending, err := m.PollIndexReset(request, "admin", "reset-async-test")
	again, againErr := m.PollIndexReset(request, "admin", "reset-async-test")
	elapsed := time.Since(start)
	m.mu.Unlock()
	if err != nil || againErr != nil || pending.Completed || pending.ResetID == "" || again.ResetID != pending.ResetID || elapsed > time.Second {
		t.Fatal(pending, again, err, againErr, elapsed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		result, e := m.PollIndexReset(request, "admin", "reset-async-test")
		if e != nil {
			t.Fatal(e)
		}
		if result.Completed {
			replay, e := m.PollIndexReset(request, "admin", "reset-async-test")
			if e != nil || replay != result {
				t.Fatal(replay, e)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("reset did not complete")
}

// The same key cannot be redirected to another source scope even while its first call is pending.
func TestIndexResetPollingRejectsChangedPayload(t *testing.T) {
	m, _, _ := bootFixture(t)
	request := IndexResetRequest{ContractVersion: 1, All: true, Confirmed: true}
	if _, err := m.PollIndexReset(request, "admin", "reset-conflict-test"); err != nil {
		t.Fatal(err)
	}
	request.All = false
	request.Scope = &contracts.Scope{}
	// Use a valid NAS selector to keep this a payload-conflict check.
	request.Scope.Kind = "nas"
	request.Scope.TargetIDs = nil
	request.Scope.RelativePath = ""
	if _, err := m.PollIndexReset(request, "admin", "reset-conflict-test"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal(err)
	}
}
