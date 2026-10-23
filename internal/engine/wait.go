package engine

import "context"

// WaitIdle observes the operation active at entry, without canceling work,
// changing state, retrying approval or granting authority for the next action.
// Callers still perform each independent approval using its current revision.
func (e *Engine) WaitIdle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op == nil {
		return nil
	}
	select {
	case <-op.finished:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
