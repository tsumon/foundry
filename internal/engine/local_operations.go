package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"path/filepath"
)

func supportedMode(mode string) error {
	base, _, err := domain.BaseWorkspaceMode(mode)
	if err != nil {
		return err
	}
	if base != "" && !domain.UsesGenerations(mode) {
		return errors.New("unsupported workspace mode; execution disabled")
	}
	return nil
}

// selectedCandidate is the only resolver for selected candidate content. It
// verifies selection and disk seals without treating either as approval.
func (e *Engine) selectedCandidate(m domain.Mission) (string, error) {
	if err := supportedMode(m.WorkspaceMode); err != nil {
		return "", err
	}
	path := filepath.Join(e.dir(m.ID), "candidate")
	if domain.UsesGenerations(m.WorkspaceMode) {
		if m.CandidateGeneration == nil || m.CandidateGeneration.Digest != m.CandidateDigest {
			return "", errors.New("candidate generation binding missing or changed")
		}
		if err := workspace.VerifyGeneration(e.dir(m.ID), *m.CandidateGeneration); err != nil {
			return "", err
		}
		var err error
		path, err = workspace.GenerationPath(e.dir(m.ID), *m.CandidateGeneration)
		if err != nil {
			return "", err
		}
	}
	digest, err := workspace.Digest(path)
	if err != nil {
		return "", err
	}
	if digest != m.CandidateDigest {
		return "", errors.New("candidate changed outside controller")
	}
	return path, nil
}

func intentDigest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (e *Engine) localIntent(m domain.Mission, kind string, input any) (domain.LocalOperation, error) {
	id, err := opaque()
	if err != nil {
		return domain.LocalOperation{}, err
	}
	base, err := workspace.Digest(filepath.Join(e.dir(m.ID), "base"))
	if err != nil {
		return domain.LocalOperation{}, err
	}
	approval, err := authority(m, filepath.Join(e.dir(m.ID), "base"), kind != "reset")
	if err != nil {
		return domain.LocalOperation{}, err
	}
	if m.CandidateGeneration == nil {
		return domain.LocalOperation{}, errors.New("missing generation")
	}
	op := domain.LocalOperation{ID: id, MissionID: m.ID, Kind: kind, ExpectedRevision: m.Revision, Epoch: m.Epoch, Before: *m.CandidateGeneration, BaseDigest: base, ApprovalDigest: approval}
	if m.Plan != nil {
		op.PlanRevision = m.Plan.Revision
	}
	binding := op
	binding.ID = ""
	op.IntentDigest = intentDigest(struct {
		Operation domain.LocalOperation
		Input     any
	}{binding, input})
	return op, nil
}

func (e *Engine) interruptUnfinished(m domain.Mission) error {
	ops, err := e.s.ListLocalOperations(m.ID)
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.Status == "pending" || op.Status == "prepared" {
			if _, err = e.s.InterruptLocalOperation(op.ID, "interrupted; explicit action required"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) commitLocal(op domain.LocalOperation, event string, mutate func(*domain.Mission) error) (domain.Mission, error) {
	m, err := e.s.CommitLocalOperation(op.ID, event, mutate)
	if err == nil {
		return m, nil
	}
	persisted, readErr := e.s.GetLocalOperation(op.ID)
	if readErr == nil && persisted.Status == "committed" {
		return e.s.GetMission(op.MissionID)
	}
	if readErr == nil && (persisted.Status == "pending" || persisted.Status == "prepared") {
		_, _ = e.s.InterruptLocalOperation(op.ID, "selection not committed: "+err.Error())
	}
	return m, err
}

func resetRun(m *domain.Mission, revision int64, ref domain.GenerationRef) {
	if m.State != "draft" {
		planRevision := int64(0)
		if m.Plan != nil {
			planRevision = m.Plan.Revision
		}
		m.RunArchives = append(m.RunArchives, domain.RunArchive{Epoch: m.Epoch, PlanRevision: planRevision, Revision: m.Revision, ArchivedAt: now(), State: m.State, Error: m.Error, ImageID: m.Spec.ImageID, CandidateDigest: m.CandidateDigest, CandidateGeneration: m.CandidateGeneration, Plan: m.Plan, Research: m.Research, Changes: m.Changes, Evidence: m.Evidence, Reviews: m.Reviews, Delivery: m.Delivery})
	}
	m.Epoch++
	m.State = "planning"
	m.Stage = "plan"
	m.Error = ""
	m.Plan = nil
	m.Research = nil
	m.Changes = []domain.Change{}
	m.Evidence = []domain.Verification{}
	m.Reviews = []domain.Review{}
	m.CandidateGeneration = &ref
	m.CandidateDigest = ref.Digest
	m.Delivery = nil
	m.Spec.ImageID = ""
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest, Revision: revision, CreatedAt: now()})
}

func (e *Engine) resetGeneration(m domain.Mission) (domain.Mission, error) {
	if err := e.interruptUnfinished(m); err != nil {
		return m, err
	}
	op, err := e.localIntent(m, "reset", nil)
	if err != nil {
		return m, err
	}
	op, err = e.s.BeginLocalOperation(op)
	if err != nil {
		return m, err
	}
	ref, _, err := workspace.PrepareGeneration(e.dir(m.ID), op.ID, filepath.Join(e.dir(m.ID), "base"), m.CandidateGeneration, nil, m.Spec.Scope, m.Spec.Protected)
	if err != nil {
		_, _ = e.s.InterruptLocalOperation(op.ID, err.Error())
		return m, err
	}
	prepared, err := e.s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref, Changes: []domain.Change{}})
	if err != nil {
		_, _ = e.s.InterruptLocalOperation(op.ID, err.Error())
		return m, err
	}
	return e.commitLocal(prepared, "mission.planning", func(current *domain.Mission) error { resetRun(current, m.Revision, ref); return nil })
}

func (e *Engine) applyGeneration(run *operation, out editOutput, files []string) error {
	// Apply has the same cardinality contract as the legacy workspace path.
	// PrepareGeneration also serves create/reset, where empty edits are valid.
	if len(out.Edits) == 0 || len(out.Edits) > 30 {
		return e.denyModelValidation(run, "invalid_edit_count", errors.New("edits must contain 1–30 files"))
	}
	e.mu.Lock()
	m, err := e.s.GetMission(run.id)
	if err == nil && (run.ctx.Err() != nil || m.Epoch != run.epoch) {
		err = context.Canceled
	}
	if err == nil {
		_, err = e.candidate(m)
	}
	allowed := map[string]bool{}
	for _, p := range files {
		allowed[p] = true
	}
	edits := []workspace.Edit{}
	invalidProposal := false
	if err == nil {
		for _, edit := range out.Edits {
			if edit.Content == nil || !allowed[edit.Path] {
				err = errors.New("edit missing content or outside exact step files")
				invalidProposal = true
				break
			}
			edits = append(edits, workspace.Edit{Path: edit.Path, Content: *edit.Content})
		}
	}
	var op domain.LocalOperation
	if err == nil {
		op, err = e.localIntent(m, "apply", edits)
	}
	if err == nil {
		op, err = e.s.BeginLocalOperation(op)
	}
	e.mu.Unlock()
	if err != nil {
		if invalidProposal {
			return e.denyModelValidation(run, "invalid_edit_scope", err)
		}
		return err
	}
	path, err := e.selectedCandidate(m)
	var ref domain.GenerationRef
	var delta []domain.Change
	if err == nil {
		ref, delta, err = workspace.PrepareGeneration(e.dir(m.ID), op.ID, path, m.CandidateGeneration, edits, m.Spec.Scope, m.Spec.Protected)
	}
	if err != nil {
		_, _ = e.s.InterruptLocalOperation(op.ID, err.Error())
		var invalid *workspace.ProposalValidationError
		if errors.As(err, &invalid) {
			return e.denyModelValidation(run, "invalid_edit_proposal", err)
		}
		return err
	}
	changes := append(append([]domain.Change{}, m.Changes...), delta...)
	prepared, err := e.s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref, Changes: changes})
	if err != nil {
		_, _ = e.s.InterruptLocalOperation(op.ID, err.Error())
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if run.ctx.Err() != nil {
		_, _ = e.s.InterruptLocalOperation(op.ID, "run cancelled")
		return context.Canceled
	}
	_, err = e.commitLocal(prepared, "candidate.edited", func(current *domain.Mission) error {
		current.CandidateGeneration = &ref
		current.CandidateDigest = ref.Digest
		current.Changes = changes
		return nil
	})
	return err
}

func exportMetadata(p domain.PreparedExport) workspace.PreparedExport {
	return workspace.PreparedExport{PatchPath: p.PatchPath, ReportPath: p.ReportPath, PatchSHA256: p.PatchSHA256, ReportSHA256: p.ReportSHA256}
}

func (e *Engine) verifyDelivery(m domain.Mission) error {
	if m.Delivery == nil {
		return errors.New("missing delivery")
	}
	op, err := e.s.GetLocalOperation(m.Delivery.OperationID)
	if err != nil {
		return err
	}
	if op.MissionID != m.ID || op.Kind != "export" || op.Status != "committed" || op.Result == nil || op.Result.Export == nil || m.CandidateGeneration == nil || op.Before != *m.CandidateGeneration || m.Delivery.CandidateDigest != m.CandidateDigest {
		return errors.New("delivery operation binding changed")
	}
	p := op.Result.Export
	if m.Delivery.PatchPath != p.PatchPath || m.Delivery.ReportPath != p.ReportPath || m.Delivery.PatchSHA256 != p.PatchSHA256 || m.Delivery.ReportSHA256 != p.ReportSHA256 {
		return errors.New("delivery artifact binding changed")
	}
	return workspace.VerifyExportBinding(e.dir(m.ID), op.ID, exportMetadata(*p), op.Before, op.BaseDigest)
}

func (e *Engine) exportIntent(m domain.Mission) (domain.LocalOperation, error) {
	report := marshalReport(m)
	proposed, err := e.localIntent(m, "export", struct {
		Report    string
		Evidence  []domain.Verification
		Reviews   []domain.Review
		Approvals []domain.Approval
	}{report, m.Evidence, m.Reviews, m.Approvals})
	if err != nil {
		return proposed, err
	}
	proposed.ExportReport = report
	return proposed, nil
}

func (e *Engine) deliverGeneration(ctx context.Context, m domain.Mission) (domain.Mission, error) {
	proposed, err := e.exportIntent(m)
	if err != nil {
		return m, err
	}
	ops, err := e.s.ListLocalOperations(m.ID)
	if err != nil {
		return m, err
	}
	op := proposed
	for _, prior := range ops {
		if prior.Kind != "export" || prior.Status == "committed" {
			continue
		}
		if prior.ExpectedRevision != m.Revision {
			if prior.Status == "pending" || prior.Status == "prepared" {
				if _, err = e.s.InterruptLocalOperation(prior.ID, "stale revision; explicit fresh delivery required"); err != nil {
					return m, err
				}
			}
			continue
		}
		if prior.IntentDigest != proposed.IntentDigest || prior.ExportReport != proposed.ExportReport {
			return m, errors.New("existing export intent does not match current delivery")
		}
		op = prior
		break
	}
	if op.Status == "" {
		op, err = e.s.BeginLocalOperation(op)
		if err != nil {
			return m, err
		}
	}
	candidate, err := e.selectedCandidate(m)
	if err != nil {
		return m, err
	}
	prepared, err := workspace.PrepareExport(ctx, e.dir(m.ID), op.ID, candidate, op.ExportReport)
	if err != nil {
		if op.Status != "interrupted" {
			_, _ = e.s.InterruptLocalOperation(op.ID, err.Error())
		}
		return m, err
	}
	result := domain.LocalOperationResult{Export: &domain.PreparedExport{PatchPath: prepared.PatchPath, ReportPath: prepared.ReportPath, PatchSHA256: prepared.PatchSHA256, ReportSHA256: prepared.ReportSHA256, ReportInput: op.ExportReport}}
	if op.Status == "prepared" {
		if intentDigest(op.Result) != intentDigest(&result) {
			return m, errors.New("prepared export mismatch")
		}
	} else {
		registered, prepareErr := e.s.PrepareLocalOperation(op.ID, result)
		if prepareErr != nil {
			if op.Status != "interrupted" {
				_, _ = e.s.InterruptLocalOperation(op.ID, prepareErr.Error())
			}
			return m, prepareErr
		}
		op = registered
	}
	return e.commitLocal(op, "delivery.exported", func(current *domain.Mission) error {
		current.State = "delivered"
		current.Stage = "deliver"
		current.Delivery = &domain.Delivery{OperationID: op.ID, PatchPath: prepared.PatchPath, ReportPath: prepared.ReportPath, PatchSHA256: prepared.PatchSHA256, ReportSHA256: prepared.ReportSHA256, CandidateDigest: m.CandidateDigest, CreatedAt: now()}
		current.Approvals = append(current.Approvals, domain.Approval{Kind: "deliver", Digest: m.CandidateDigest, Revision: m.Revision, CreatedAt: now()})
		return nil
	})
}
