package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"time"
)

func (e *Engine) PrepareC4Readiness(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	return e.prepareC4Control(ctx, id, revision, "readiness", "", "", "")
}
func (e *Engine) ExecuteC4Readiness(ctx context.Context, id string, revision int64, controlID, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, c, err := e.dispatchC4Control(ctx, id, revision, controlID, digest, "readiness")
	if err != nil {
		return m, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ready, runErr := e.options.C4Controlled.readiness(bounded)
	r := domain.C4ControlResult{Status: "failed", FailureCode: "failed"}
	deadline, _ := time.Parse(time.RFC3339Nano, c.ActivationExpiresAt)
	if runErr == nil && bounded.Err() == nil && e.c4Time().Before(deadline) {
		until := e.c4Time().Add(domain.C4ControlTTL)
		if deadline.Before(until) {
			until = deadline
		}
		r = domain.C4ControlResult{Status: "ready", Fingerprint: ready.Fingerprint, ValidUntil: until.Format(time.RFC3339Nano)}
	} else if errors.Is(runErr, context.Canceled) || errors.Is(bounded.Err(), context.Canceled) {
		r.FailureCode = "canceled"
	} else if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
		r.FailureCode = "timeout"
	}
	m, err = e.finishC4Control(m, c, r)
	if err != nil {
		return m, sandbox.ErrC4Persistence
	}
	if r.Status != "ready" {
		return m, ErrC4Unavailable
	}
	return m, nil
}

func (e *Engine) currentC4Readiness(m domain.Mission) (domain.C4Control, error) {
	if e.c4ControlledGate(m, true) != nil {
		return domain.C4Control{}, ErrC4Unavailable
	}
	for i := len(m.C4Controls) - 1; i >= 0; i-- {
		c := m.C4Controls[i]
		if c.Kind != "readiness" {
			continue
		}
		// Latest attempted material supersedes previous authority; no fallback.
		if c.Result == nil || c.Result.Status != "ready" || c.ActivationDigest != e.options.C4Controlled.config.Digest() || c.ActivationExpiresAt != e.options.C4Controlled.config.ExpiresAt || c.SourceKind != e.options.C4Controlled.source || c.BaseCommit != m.BaseCommit || c.BaseDigest != m.RepositoryImport.BaseDigest || c.ProfileDigest != m.Spec.C4.ProfileDigest() {
			return domain.C4Control{}, ErrC4Unavailable
		}
		until, err := time.Parse(time.RFC3339Nano, c.Result.ValidUntil)
		if err != nil || !e.c4Time().Before(until) {
			return domain.C4Control{}, ErrC4Unavailable
		}
		return c, nil
	}
	return domain.C4Control{}, ErrC4Unavailable
}
