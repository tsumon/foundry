package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/workspace"
	"path/filepath"
	"time"
)

func (e *Engine) PrepareC4Cleanup(ctx context.Context, id string, revision int64, operationID, digest, exactID string) (domain.Mission, error) {
	return e.prepareC4Control(ctx, id, revision, "cleanup", operationID, digest, exactID)
}
func (e *Engine) c4CleanupTarget(m domain.Mission, c domain.C4Control) (domain.C4Operation, string, error) {
	o, err := c4Operation(m, c.OperationID, c.OperationDigest)
	if err != nil || o.Result == nil || o.Dispatch == nil || o.ContainerID == "" || o.ContainerID != c.ContainerID || o.CleanupState() == "confirmed" || o.BaseCommit != m.BaseCommit || o.BaseDigest != m.RepositoryImport.BaseDigest || o.ProfileDigest != m.Spec.C4.ProfileDigest() {
		return o, "", ErrC4Unavailable
	}
	receipt, err := c4Control(m, o.ReadinessID, o.ReadinessDigest)
	if err != nil || receipt.SourceKind != c.SourceKind {
		return o, "", ErrC4Unavailable
	}
	path := filepath.Join(e.dir(m.ID), "base")
	if o.Kind == "candidate" {
		if o.CandidateGeneration == nil {
			return o, "", ErrC4Unavailable
		}
		if err = workspace.VerifyGeneration(e.dir(m.ID), *o.CandidateGeneration); err != nil {
			return o, "", ErrC4Unavailable
		}
		path, err = workspace.GenerationPath(e.dir(m.ID), *o.CandidateGeneration)
		if err != nil {
			return o, "", err
		}
	}
	path, err = e.c4Path(path)
	if err != nil {
		return o, "", err
	}
	// The original immutable path and exact ownership plan are checked before any transport.
	if _, err = sandbox.BuildC4Plan(*m.Spec.C4, o, e.c4Trust(m), path); err != nil {
		return o, "", err
	}
	return o, path, nil
}
func (e *Engine) ExecuteC4Cleanup(ctx context.Context, id string, revision int64, controlID, digest string) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, c, err := e.dispatchC4Control(ctx, id, revision, controlID, digest, "cleanup")
	if err != nil {
		return m, err
	}
	o, path, err := e.c4CleanupTarget(m, c)
	if err != nil {
		return m, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cap := e.options.C4Controlled
	switch cap.sourceFor(m.Spec.C4) {
	case "embedded_vm_c4_control":
		if m.Spec.C4.SchemaVersion == 2 {
			var inputs domain.C4InputContractV2
			var root string
			inputs, root, _, err = e.resolveV2MaterialMode(bounded, m, false)
			if err == nil {
				_, err = sandbox.CleanupC4VMV2(bounded, *m.Spec.C4, o, cap.vmForProfile(*m.Spec.C4), path, c.ContainerID, inputs, root, e.c4PersistLocked(id))
			}
		} else {
			_, err = sandbox.CleanupC4VM(bounded, *m.Spec.C4, o, cap.vmForProfile(*m.Spec.C4), path, c.ContainerID, e.c4PersistLocked(id))
		}
	case "docker_c4_control":
		_, err = sandbox.CleanupC4Docker(bounded, *m.Spec.C4, o, sandbox.C4DockerConfig{Enabled: true, Trust: e.c4Trust(m)}, path, c.ContainerID, e.c4PersistLocked(id))
	case "synthetic_c4_control":
		_, err = sandbox.CleanupC4(bounded, *m.Spec.C4, o, e.c4Trust(m), c.ContainerID, cap.driver, e.c4PersistLocked(id))
	default:
		err = ErrC4Unavailable
	}
	current, getErr := e.s.GetMission(id)
	if getErr != nil {
		return m, sandbox.ErrC4Persistence
	}
	if err == sandbox.ErrC4Persistence {
		return current, err
	}
	r := domain.C4ControlResult{Status: "cleaned"}
	if err != nil {
		r.Status = "failed"
		r.FailureCode = "failed"
	}
	current, saveErr := e.finishC4Control(current, c, r)
	if saveErr != nil {
		return current, sandbox.ErrC4Persistence
	}
	return current, err
}
