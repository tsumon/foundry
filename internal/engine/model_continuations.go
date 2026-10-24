package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/store"
	"time"
)

// These metadata-only operations deliberately bypass runtime readiness,
// preflight, credential resolution and filesystem execution. Each gate after
// continuation still requires its own explicit approval.
func (e *Engine) PrepareModelContinuation(ctx context.Context, id string, revision int64, authorizationID, catalogID string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.modelContinuationIdle(ctx); err != nil {
		return domain.Mission{}, err
	}
	return e.s.PrepareModelContinuation(id, revision, authorizationID, catalogID, time.Now())
}
func (e *Engine) ApproveModelContinuation(ctx context.Context, id, continuationID string, revision int64, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.modelContinuationIdle(ctx); err != nil {
		return domain.Mission{}, err
	}
	return e.s.ApproveModelContinuation(id, continuationID, revision, digest, time.Now())
}
func (e *Engine) ApplyModelContinuation(ctx context.Context, id, continuationID string, revision int64, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.modelContinuationIdle(ctx); err != nil {
		return domain.Mission{}, err
	}
	return e.s.ApplyModelContinuation(id, continuationID, revision, digest, time.Now())
}
func (e *Engine) modelContinuationIdle(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.closed || e.active != nil {
		return store.ErrConflict
	}
	return nil
}
