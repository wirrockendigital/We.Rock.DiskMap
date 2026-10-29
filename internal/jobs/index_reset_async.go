package jobs

import (
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// A bounded operation slot publishes its terminal result through a closed channel.
type resetOperation struct {
	payload   string
	ready     chan struct{}
	receipt   IndexResetReceipt
	err       error
	pendingID string
}

// PollIndexReset detaches expensive copies from DSM's short CGI response deadline.
// The durable replay record still owns mutation identity across process restarts.
func (m *Manager) PollIndexReset(in IndexResetRequest, principal, key string) (IndexResetReceipt, error) {
	raw, _ := json.Marshal(in)
	replay := ReplayRequest{Principal: principal, Operation: "index.reset", Key: key, Payload: string(raw)}
	if in.Validate() != nil || replay.Validate() != nil {
		return IndexResetReceipt{}, ErrInvalid
	}
	m.resetMu.Lock()
	defer m.resetMu.Unlock()
	identity := principal + ":" + key
	if op := m.resetOperations[identity]; op != nil {
		if op.payload != string(raw) {
			return IndexResetReceipt{}, ErrIdempotencyConflict
		}
		select {
		case <-op.ready:
			return op.receipt, op.err
		default:
			return IndexResetReceipt{ResetID: op.pendingID}, nil
		}
	}
	// Serialize mutations and retain only a small set of completed delivery receipts.
	for _, op := range m.resetOperations {
		select {
		case <-op.ready:
		default:
			return IndexResetReceipt{}, ErrUnavailable
		}
	}
	if len(m.resetOperations) >= 8 {
		for id := range m.resetOperations {
			delete(m.resetOperations, id)
			break
		}
	}
	if m.resetOperations == nil {
		m.resetOperations = map[string]*resetOperation{}
	}
	pendingID, err := profiles.NewID()
	if err != nil {
		return IndexResetReceipt{}, err
	}
	op := &resetOperation{payload: string(raw), ready: make(chan struct{}), pendingID: pendingID}
	m.resetOperations[identity] = op
	go func() { op.receipt, op.err = m.ResetIndex(m.ctx, in, principal, key); close(op.ready) }()
	return IndexResetReceipt{ResetID: pendingID}, nil
}
