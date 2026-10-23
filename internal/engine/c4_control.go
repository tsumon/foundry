package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/store"
	"time"
)

func c4Control(m domain.Mission, id, digest string) (domain.C4Control, error) {
	for _, c := range m.C4Controls {
		if c.ID == id && c.MaterialDigest == digest {
			return c, nil
		}
	}
	return domain.C4Control{}, store.ErrConflict
}
func (e *Engine) c4ControlCurrent(m domain.Mission, c domain.C4Control) error {
	if e.c4ControlledGate(m, true) != nil || c.MissionID != m.ID || c.Epoch != m.Epoch || c.PlanRevision != c4PlanRevision(m) || c.BaseCommit != m.BaseCommit || c.BaseDigest != m.RepositoryImport.BaseDigest || c.ProfileDigest != m.Spec.C4.ProfileDigest() || c.ActivationDigest != e.c4Activation(m).Digest() || c.ActivationExpiresAt != e.options.C4Controlled.config.ExpiresAt || c.SourceKind != e.options.C4Controlled.source || c.MaterialDigest != domain.C4ControlDigest(c) {
		return ErrC4Unavailable
	}
	expiry, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if err != nil || !e.c4Time().Before(expiry) {
		return ErrC4Unavailable
	}
	if err = e.checkRepositoryImportBase(m); err != nil {
		return err
	}
	if err = e.checkFrozenAuthority(m); err != nil {
		return err
	}
	if c.Kind == "cleanup" {
		_, _, err = e.c4CleanupTarget(m, c)
		return err
	}
	return nil
}

func (e *Engine) newC4Control(m domain.Mission, kind string) (domain.C4Control, error) {
	if e.c4ControlledGate(m, true) != nil {
		return domain.C4Control{}, ErrC4Unavailable
	}
	id, err := opaque()
	if err != nil {
		return domain.C4Control{}, err
	}
	c := e.options.C4Controlled
	at := e.c4Time()
	deadline, _ := time.Parse(time.RFC3339Nano, c.config.ExpiresAt)
	expiry := at.Add(domain.C4ControlTTL)
	if deadline.Before(expiry) {
		expiry = deadline
	}
	return domain.C4Control{SchemaVersion: 1, ID: id, Kind: kind, MissionID: m.ID, Epoch: m.Epoch, PlanRevision: c4PlanRevision(m), BaseCommit: m.BaseCommit, BaseDigest: m.RepositoryImport.BaseDigest, ProfileDigest: m.Spec.C4.ProfileDigest(), ActivationDigest: e.c4Activation(m).Digest(), ActivationExpiresAt: c.config.ExpiresAt, SourceKind: c.source, PreparedRevision: m.Revision, PreparedAt: at.Format(time.RFC3339Nano), ExpiresAt: expiry.Format(time.RFC3339Nano), State: "prepared"}, nil
}

func (e *Engine) prepareC4Control(ctx context.Context, id string, revision int64, kind, operationID, digest, exactID string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	// The new model-backed profile has a terminal delivery. Appending a new
	// readiness/cleanup control would change its execution/report binding.
	if isModelC4(m) && m.Delivery != nil {
		return m, ErrC4Unavailable
	}
	if e.active != nil || ctx.Err() != nil {
		return m, ErrC4Unavailable
	}
	// Re-preparation would advance the revision and strand a baseline whose
	// one-shot authority has not been dispatched. There is no abandon workflow.
	if kind == "readiness" {
		for _, o := range m.C4Operations {
			if o.Kind == "baseline" && o.Dispatch == nil && o.Result == nil {
				return m, ErrC4Unavailable
			}
		}
	}
	c, err := e.newC4Control(m, kind)
	if err != nil {
		return m, err
	}
	c.OperationID, c.OperationDigest, c.ContainerID = operationID, digest, exactID
	c.MaterialDigest = domain.C4ControlDigest(c)
	if err = e.c4ControlCurrent(m, c); err != nil {
		return m, err
	}
	return e.s.UpdateMission(id, revision, "c4.control_prepared", func(current *domain.Mission) error { current.C4Controls = append(current.C4Controls, c); return nil })
}

func (e *Engine) ApproveC4Control(ctx context.Context, id string, revision int64, controlID, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	c, err := c4Control(m, controlID, digest)
	if err != nil {
		return m, err
	}
	if ctx.Err() != nil || e.active != nil || c.State != "prepared" || m.Revision != c.PreparedRevision+1 {
		return m, store.ErrConflict
	}
	if err = e.c4ControlCurrent(m, c); err != nil {
		return m, err
	}
	return e.s.UpdateMission(id, revision, "c4.control_approved", func(current *domain.Mission) error {
		for i := range current.C4Controls {
			if current.C4Controls[i].ID == c.ID {
				current.C4Controls[i].Approval = &domain.C4Approval{Digest: digest, Revision: current.Revision, At: e.c4Timestamp()}
				current.C4Controls[i].State = "approved"
				return nil
			}
		}
		return store.ErrConflict
	})
}

// Caller holds e.mu through dispatch, the bounded effect and outcome persistence.
func (e *Engine) dispatchC4Control(ctx context.Context, id string, revision int64, controlID, digest, kind string) (domain.Mission, domain.C4Control, error) {
	m, err := e.checked(id, revision)
	if err != nil {
		return m, domain.C4Control{}, err
	}
	c, err := c4Control(m, controlID, digest)
	if err != nil {
		return m, c, err
	}
	if ctx.Err() != nil || e.active != nil || c.Kind != kind || c.State != "approved" || c.Approval == nil || m.Revision != c.Approval.Revision+1 {
		return m, c, store.ErrConflict
	}
	if err = e.c4ControlCurrent(m, c); err != nil {
		return m, c, err
	}
	m, err = e.s.UpdateMission(id, revision, "c4.control_dispatched", func(current *domain.Mission) error {
		for i := range current.C4Controls {
			if current.C4Controls[i].ID == c.ID {
				current.C4Controls[i].Dispatch = &domain.C4Fact{Revision: current.Revision, At: e.c4Timestamp()}
				current.C4Controls[i].State = "dispatched"
				return nil
			}
		}
		return store.ErrConflict
	})
	return m, c, err
}
func (e *Engine) finishC4Control(m domain.Mission, c domain.C4Control, r domain.C4ControlResult) (domain.Mission, error) {
	return e.s.UpdateMission(m.ID, m.Revision, "c4.control_completed", func(current *domain.Mission) error {
		for i := range current.C4Controls {
			if current.C4Controls[i].ID == c.ID {
				r.Revision = current.Revision
				r.At = e.c4Timestamp()
				current.C4Controls[i].Result = &r
				current.C4Controls[i].State = "completed"
				return nil
			}
		}
		return store.ErrConflict
	})
}
