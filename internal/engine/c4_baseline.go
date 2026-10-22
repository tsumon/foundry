package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"path/filepath"
)

func (e *Engine) PrepareC4Baseline(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	if m.Spec.C4 == nil || e.active != nil || (m.State != "draft" && m.State != "blocked" && m.State != "paused") {
		return m, ErrC4Unavailable
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	if err = e.checkFrozenAuthority(m); err != nil {
		return m, err
	}
	o, err := e.newC4Operation(m, "baseline")
	if err != nil {
		return m, err
	}
	return e.s.UpdateMission(id, revision, "c4.prepared", func(current *domain.Mission) error {
		if err := e.c4Current(*current, o); err != nil {
			return err
		}
		current.C4Operations = append(current.C4Operations, o)
		return nil
	})
}
func (e *Engine) ApproveC4Baseline(ctx context.Context, id string, revision int64, operationID, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	if e.active != nil {
		return m, errors.New("another operation is active")
	}
	o, err := c4Operation(m, operationID, digest)
	if err != nil {
		return m, err
	}
	if o.Kind != "baseline" || o.State != "prepared" {
		return m, ErrC4Unavailable
	}
	if m.Revision != c4PreparedRevision(o)+1 {
		return m, store.ErrConflict
	}
	if err = ctx.Err(); err != nil {
		return m, err
	}
	return e.s.UpdateMission(id, revision, "c4.approved", func(current *domain.Mission) error {
		if err := e.c4Current(*current, o); err != nil {
			return err
		}
		for i := range current.C4Operations {
			if current.C4Operations[i].ID == o.ID {
				current.C4Operations[i].Approval = &domain.C4Approval{Digest: digest, Revision: current.Revision, At: e.c4Timestamp()}
				current.C4Operations[i].State = "approved"
				return nil
			}
		}
		return ErrC4Unavailable
	})
}

func (e *Engine) ExecuteC4Baseline(ctx context.Context, id string, revision int64, operationID, digest string) (domain.Mission, error) {
	return e.runC4Explicit(ctx, id, revision, operationID, digest, "", false)
}
func (e *Engine) CleanupC4Operation(ctx context.Context, id string, revision int64, operationID, digest, exactID string) (domain.Mission, error) {
	return e.runC4Explicit(ctx, id, revision, operationID, digest, exactID, true)
}
func (e *Engine) runC4Explicit(ctx context.Context, id string, revision int64, operationID, digest, exactID string, cleanup bool) (domain.Mission, error) {
	e.mu.Lock()
	m, err := e.checked(id, revision)
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	if cleanup && m.Spec.C4 != nil && m.Spec.C4.ControlVersion == 1 {
		e.mu.Unlock()
		return m, ErrC4Unavailable
	}
	o, err := c4Operation(m, operationID, digest)
	if err != nil {
		e.mu.Unlock()
		return m, err
	}
	if e.active != nil || m.Spec.C4 == nil || (!cleanup && (o.Kind != "baseline" || o.State != "approved")) || (cleanup && (o.Result == nil || o.CleanupState() == "confirmed" || exactID == "")) {
		e.mu.Unlock()
		return m, ErrC4Unavailable
	}
	if !cleanup {
		if o.Approval == nil || m.Revision != o.Approval.Revision+1 {
			e.mu.Unlock()
			return m, store.ErrConflict
		}
		if err = e.c4Current(m, o); err != nil {
			e.mu.Unlock()
			return m, err
		}
	}
	runCtx, cancel := context.WithCancel(ctx)
	op := &operation{id: id, epoch: m.Epoch, ctx: runCtx, cancel: cancel, done: make(chan struct{}), finished: make(chan struct{})}
	e.active = op
	e.mu.Unlock()
	defer func() {
		cancel()
		e.mu.Lock()
		close(op.done)
		if !op.pausing {
			if e.active == op {
				e.active = nil
			}
			close(op.finished)
		}
		e.mu.Unlock()
	}()
	if cleanup {
		_, err = sandbox.CleanupC4(runCtx, *m.Spec.C4, o, e.c4Trust(), exactID, e.options.C4.driver, e.c4Persist(id))
	} else {
		base, pathErr := e.c4Path(filepath.Join(e.dir(id), "base"))
		if pathErr != nil {
			return m, pathErr
		}
		_, err = e.executeC4(runCtx, m, o, base)
	}
	current, getErr := e.s.GetMission(id)
	if err != nil {
		return current, err
	}
	return current, getErr
}
