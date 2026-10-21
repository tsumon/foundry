package engine

import (
	"context"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"time"
)

// InspectRepository performs one explicitly consented, bounded, read-only source
// inspection. Only its immutable receipt is written; no mission or runner exists.
func (e *Engine) InspectRepository(parent context.Context, request domain.RepositoryInspectionRequest) (domain.RepositoryInspection, error) {
	zero := domain.RepositoryInspection{}
	if err := domain.ValidateRepositoryInspectionRequest(request); err != nil {
		return zero, err
	}
	if workspace.ValidateRules(request.Spec.Scope) != nil || len(request.Spec.Protected) > 0 && workspace.ValidateRules(request.Spec.Protected) != nil {
		return zero, domain.ImportInvalidRequest
	}
	deadline := time.Now().Add(30 * time.Second)
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	journalCtx, journalCancel := context.WithDeadline(context.WithoutCancel(parent), deadline)
	defer journalCancel()
	source := request.RepoPath
	if request.RepositoryID != "" {
		repo, err := e.s.GetGitRepositoryForInspection(journalCtx, request.RepositoryID)
		if errors.Is(err, store.ErrNotFound) {
			return zero, domain.ImportSourceChanged
		}
		if err != nil {
			if journalCtx.Err() != nil {
				return zero, domain.ImportTimeout
			}
			return zero, domain.ImportStorageFailed
		}
		if repo.Revision != request.RepositoryRevision {
			return zero, domain.ImportSourceChanged
		}
		source = repo.Path
	}
	id, err := opaque()
	if err != nil {
		return zero, domain.ImportInspectionFailed
	}
	v := domain.RepositoryInspection{SchemaVersion: 1, ID: id, RepositoryID: request.RepositoryID, RepositoryRevision: request.RepositoryRevision, Spec: request.Spec, PolicyVersion: "committed_text_v1", Status: "blocked", Stage: "identity", WorkingTreeStatus: "not_checked"}
	observation, inspectErr := gitinspect.Inspect(ctx, source)
	if inspectErr != nil {
		switch observation.ErrorCode {
		case "unsupported_layout":
			v.Code = domain.ImportLayout
		case "timeout":
			v.Code = domain.ImportTimeout
		case "canceled":
			v.Code = domain.ImportCanceled
		case "git_unavailable":
			v.Code = domain.ImportGitUnavailable
		case "invalid_repository", "repository_unavailable":
			v.Code = domain.ImportUnavailable
		default:
			v.Code = domain.ImportInspectionFailed
		}
	} else {
		v.Root = observation.Root
		v.Branch = observation.Branch
		v.Commit = observation.HEAD
		if observation.Unborn || observation.HEAD == "" {
			v.Code = domain.ImportMissingHEAD
		} else if err = workspace.CheckLocation(ctx, v.Root, e.options.DataDir); err != nil {
			v.Stage = "location"
			v.Code = domain.ImportLocation
		} else {
			v.Stage = "tree"
			facts, err := workspace.InspectCommittedTree(ctx, v.Root, v.Commit, v.Spec)
			if err != nil {
				var code domain.ImportError
				if errors.As(err, &code) {
					v.Code = code
				} else {
					v.Code = domain.ImportInspectionFailed
				}
				if v.Code == domain.ImportAcceptance {
					v.Stage = "acceptance"
				}
			} else {
				v.Status = "ready"
				v.Stage = "complete"
				v.FileCount = &facts.FileCount
				v.TotalBytes = &facts.TotalBytes
				n := len(facts.ContextManifest)
				v.ContextFileCount = &n
				v.ContextBytes = &facts.ContextBytes
				v.ContextManifest = facts.ContextManifest
				v.ProtectedInputs = facts.ProtectedInputs
			}
		}
	}
	if ctx.Err() != nil {
		v.Status = "blocked"
		if v.Stage == "complete" {
			v.Stage = "tree"
		}
		v.Code = domain.ImportCanceled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			v.Code = domain.ImportTimeout
		}
		v.FileCount = nil
		v.TotalBytes = nil
		v.ContextFileCount = nil
		v.ContextBytes = nil
		v.ContextManifest = nil
		v.ProtectedInputs = nil
	}
	v.CheckedAt = now()
	v.Digest = domain.RepositoryInspectionDigest(v)
	saved, err := e.s.SaveRepositoryInspectionContext(journalCtx, v, source)
	if errors.Is(err, domain.ImportSourceChanged) || errors.Is(err, store.ErrNotFound) {
		return zero, domain.ImportSourceChanged
	}
	if err != nil {
		if journalCtx.Err() != nil {
			return zero, domain.ImportTimeout
		}
		return zero, domain.ImportStorageFailed
	}
	return saved, nil
}
