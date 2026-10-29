package scanner

import "context"

// checkpointKey carries a job-local cooperative barrier without changing context cancellation.
type checkpointKey struct{}

// WithCheckpoint attaches the manager's pause acknowledgement boundary to metadata traversal.
func WithCheckpoint(ctx context.Context, check func() error) context.Context {
	return context.WithValue(ctx, checkpointKey{}, check)
}

// Checkpoint runs before new metadata work; an already blocked syscall must return first.
func Checkpoint(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check, ok := ctx.Value(checkpointKey{}).(func() error); ok {
		return check()
	}
	return nil
}
