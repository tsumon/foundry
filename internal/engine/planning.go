package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func boundedPlanText(field, value string, limit int) error {
	if strings.TrimSpace(value) == "" || len(value) > limit || !utf8.ValidString(value) {
		return fmt.Errorf("%s requires nonblank UTF-8 text of at most %d bytes", field, limit)
	}
	return nil
}

// validateExecutablePlan applies the same policy to model and user proposals.
func validateExecutablePlan(plan domain.Plan, spec domain.Spec) error {
	if err := boundedPlanText("summary", plan.Summary, 4000); err != nil {
		return err
	}
	if err := domain.ValidatePlan(plan, spec); err != nil {
		return err
	}
	for _, step := range plan.Steps {
		for _, field := range []struct {
			name, value string
			limit       int
		}{{"id", step.ID, 64}, {"title", step.Title, 200}, {"description", step.Description, 8000}} {
			if err := boundedPlanText(field.name, field.value, field.limit); err != nil {
				return err
			}
		}
		deps := map[string]bool{}
		for _, dep := range step.DependsOn {
			if err := boundedPlanText("dependency", dep, 64); err != nil {
				return err
			}
			if deps[dep] {
				return errors.New("duplicate dependency")
			}
			deps[dep] = true
		}
		if len(step.Files) == 0 || len(step.Files) > 30 {
			return errors.New("each step requires 1 to 30 explicit files")
		}
		files := map[string]bool{}
		for _, file := range step.Files {
			if files[file] {
				return errors.New("duplicate file")
			}
			files[file] = true
			if err := workspace.ValidateEditPath(file, spec.Scope, spec.Protected); err != nil {
				return err
			}
		}
	}
	return nil
}

func authority(m domain.Mission, base string, includePlan bool) (string, error) {
	if err := domain.ValidateCodingTrace(m); err != nil {
		return "", err
	}
	if m.CodingVersion != 0 {
		legacy := m
		legacy.CodingVersion = 0
		legacy.CodingAttempts = nil
		digest, err := authority(legacy, base, includePlan)
		if err != nil {
			return "", err
		}
		h := sha256.Sum256([]byte("FOUNDRY coding authority v1\x00" + digest))
		return hex.EncodeToString(h[:]), nil
	}
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return "", err
	}
	policySnapshot := m.ModelPolicySnapshot
	priorMission := m
	priorMission.ModelPolicySnapshot = nil
	digest, err := modelAuthority(priorMission, base, includePlan)
	if err != nil || policySnapshot == nil {
		return digest, err
	}
	h := sha256.Sum256([]byte("FOUNDRY role policy authority v1\x00" + policySnapshot.Digest + "\x00" + digest))
	return hex.EncodeToString(h[:]), nil
}

// modelAuthority is the complete pre-role-policy authority calculation. Keep
// it byte-for-byte compatible so nil provenance never changes old seals.
func modelAuthority(m domain.Mission, base string, includePlan bool) (string, error) {
	baseMode, bound, err := domain.BaseWorkspaceMode(m.WorkspaceMode)
	if err != nil {
		return "", err
	}
	if bound {
		inner := m
		inner.WorkspaceMode = baseMode
		inner.ModelSnapshot = nil
		digest, err := legacyAuthority(inner, base, includePlan)
		if err != nil {
			return "", err
		}
		prefix := "FOUNDRY account snapshot authority v1\x00"
		if m.ModelSnapshot.SchemaVersion == 2 {
			prefix = "FOUNDRY account snapshot authority v2\x00"
		}
		h := sha256.Sum256([]byte(prefix + m.WorkspaceMode + "\x00" + m.ModelSnapshot.Digest + "\x00" + digest))
		return hex.EncodeToString(h[:]), nil
	}
	return legacyAuthority(m, base, includePlan)
}

// Only authority may enter this projection for account-bound missions.
func legacyAuthority(m domain.Mission, base string, includePlan bool) (string, error) {
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return researchAuthority(m, base, includePlan)
	}
	digest, err := workspace.Digest(base)
	if err != nil {
		return "", err
	}
	if err := supportedMode(m.WorkspaceMode); err != nil {
		return "", err
	}
	spec := m.Spec
	if !includePlan {
		spec.ImageID = ""
	}
	var plan *domain.Plan
	if includePlan {
		if m.Plan != nil {
			copyPlan := *m.Plan
			copyPlan.Steps = append([]domain.Step(nil), m.Plan.Steps...)
			for i := range copyPlan.Steps {
				copyPlan.Steps[i].Status = ""
			}
			plan = &copyPlan
		}
	}
	b, err := json.Marshal(struct {
		Goal, Commit, Base string
		Spec               domain.Spec
		Profiles           map[domain.Role]domain.Profile
		Manifest           []domain.FileMeta
		Plan               *domain.Plan
	}{m.Goal, m.BaseCommit, digest, spec, m.Profiles, m.ContextManifest, plan})
	if err != nil {
		return "", err
	}
	if m.WorkspaceMode != "" {
		b = append([]byte("FOUNDRY authority workspace_mode="+m.WorkspaceMode+"\x00"), b...)
	}
	if m.ToolMode != "" {
		if m.ToolMode != domain.ToolModeControlledReadV1 || m.WorkspaceMode != domain.WorkspaceModeControlledReadV1 || m.TaskBindings == nil {
			return "", errors.New("invalid controlled mode binding")
		}
		binding, err := domain.TaskBindingDigest(*m.TaskBindings)
		if err != nil {
			return "", err
		}
		b = append([]byte("FOUNDRY controlled authority v1\x00"+binding+"\x00"), b...)
	} else if m.WorkspaceMode == domain.WorkspaceModeControlledReadV1 {
		return "", errors.New("controlled workspace requires tool mode")
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func hasSeal(m domain.Mission, kind, digest string) bool {
	for i := len(m.Approvals) - 1; i >= 0; i-- {
		if m.Approvals[i].Kind == kind {
			return m.Approvals[i].Digest == digest
		}
	}
	return false
}
func (e *Engine) checkSnapshot(m domain.Mission) error {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return err
	}
	if err := supportedMode(m.WorkspaceMode); err != nil {
		return err
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		if _, err := e.selectedCandidate(m); err != nil {
			return err
		}
		if m.Delivery != nil {
			if err := e.verifyDelivery(m); err != nil {
				return err
			}
		}
	}
	inputs, err := workspace.AcceptanceInputs(filepath.Join(e.dir(m.ID), "base"), m.Spec.Acceptance)
	if err != nil {
		return err
	}
	for _, p := range inputs {
		protected := false
		for _, rule := range m.Spec.Protected {
			protected = protected || workspace.Matches(p, rule)
		}
		if !protected {
			return errors.New("acceptance implementation is not frozen; create a new mission")
		}
	}
	if m.Budget.MaxCalls != m.Spec.MaxCalls || m.Budget.UsedCalls < 0 || m.Budget.UsedCalls > m.Budget.MaxCalls {
		return errors.New("approved budget changed")
	}
	seal, err := authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		return err
	}
	if !hasSeal(m, "snapshot", seal) {
		return errors.New("frozen snapshot/spec/provider inputs changed; create a new mission")
	}
	_, err = workspace.Context(filepath.Join(e.dir(m.ID), "base"), m.ContextManifest)
	return err
}
func (e *Engine) Plan(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	return e.plan(ctx, id, revision, false)
}
func (e *Engine) PlanControlled(ctx context.Context, id string, revision int64, consent bool) (domain.Mission, error) {
	return e.plan(ctx, id, revision, consent)
}
func (e *Engine) plan(ctx context.Context, id string, revision int64, consent bool) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	if m.ToolMode != "" && !consent {
		return m, errors.New("explicit controlled read consent required")
	}
	if e.active != nil {
		return m, errors.New("another operation is active")
	}
	if m.State != "draft" && m.State != "paused" && m.State != "blocked" {
		return m, errors.New("mission cannot plan in this state")
	}
	roles := []domain.Role{domain.Planner}
	if m.Spec.Research {
		roles = append(roles, domain.Researcher)
	}
	if err = e.preflightModelRoles(ctx, m, roles...); err != nil {
		return m, err
	}
	if err = e.checkSnapshot(m); err != nil {
		return m, err
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		if err = e.localMCPCurrent(m); err != nil {
			return m, err
		}
	}
	if domain.UsesGenerations(m.WorkspaceMode) {
		m, err = e.resetGeneration(m)
		if err != nil {
			return m, err
		}
		if m.ToolMode != "" {
			m, err = e.grantPlanningRead(m)
			if err != nil {
				return m, err
			}
		}
		e.start(m, e.planning)
		return m, nil
	}
	if err = workspace.Reset(filepath.Join(e.dir(id), "base"), filepath.Join(e.dir(id), "candidate")); err != nil {
		return m, err
	}
	digest, err := workspace.Digest(filepath.Join(e.dir(id), "candidate"))
	if err != nil {
		return m, err
	}
	m, err = e.s.UpdateMission(id, revision, "mission.planning", func(m *domain.Mission) error {
		if m.State != "draft" {
			planRevision := int64(0)
			if m.Plan != nil {
				planRevision = m.Plan.Revision
			}
			m.RunArchives = append(m.RunArchives, domain.RunArchive{
				Epoch: m.Epoch, PlanRevision: planRevision, Revision: m.Revision,
				ArchivedAt: now(), State: m.State, Error: m.Error, ImageID: m.Spec.ImageID,
				CandidateDigest: m.CandidateDigest, Plan: m.Plan, Research: m.Research,
				Changes: m.Changes, Evidence: m.Evidence, Reviews: m.Reviews, Delivery: m.Delivery,
			})
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
		m.CandidateDigest = digest
		m.Delivery = nil
		m.Spec.ImageID = ""
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest, Revision: revision, CreatedAt: now()})
		return nil
	})
	if err != nil {
		return m, err
	}
	e.start(m, e.planning)
	return m, nil
}
func (e *Engine) planning(op *operation) error {
	m, err := e.s.GetMission(op.id)
	if err != nil {
		return err
	}
	for _, a := range m.Approvals {
		if a.Kind == "execute" {
			if err := e.runner.Reconcile(op.ctx); err != nil {
				return errors.New("owned sandbox cleanup must succeed before replanning")
			}
			break
		}
	}
	image, err := e.runner.Resolve(op.ctx, m.Spec.Image)
	if err != nil {
		return err
	}
	m, err = e.update(op, "image.resolved", func(m *domain.Mission) error { m.Spec.ImageID = image; return nil })
	if err != nil {
		return err
	}
	if domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return e.researchFlow(op, domain.Invocation{})
	}
	if m.Spec.Research {
		var r domain.Research
		if m.ToolMode != "" {
			r, err = e.researchLoop(op)
		} else {
			var output legacyResearchOutput
			err = e.call(op, domain.Researcher, nil, &output)
			r = output.research()
		}
		if err != nil {
			return err
		}
		if err := validateResearch(r, m.ContextManifest); err != nil {
			return err
		}
		m, err = e.update(op, "research.completed", func(m *domain.Mission) error { m.Research = &r; return nil })
		if err != nil {
			return err
		}
	}
	var p planOutput
	if err := e.call(op, domain.Planner, nil, &p); err != nil {
		return err
	}
	plan := p.pendingPlan()
	if err := validateExecutablePlan(plan, m.Spec); err != nil {
		return e.denyModelValidation(op, "invalid_plan", err)
	}
	_, err = e.update(op, "plan.ready", func(m *domain.Mission) error {
		plan.Revision = m.Revision + 1
		m.Plan = &plan
		m.State = "awaiting_approval"
		m.Stage = "plan"
		seal, err := authority(*m, filepath.Join(e.dir(m.ID), "base"), true)
		if err != nil {
			return err
		}
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "plan_proposal", Digest: seal, Revision: plan.Revision, PlanRevision: plan.Revision, CreatedAt: now()})
		return nil
	})
	return err
}
func (e *Engine) Approve(ctx context.Context, id string, revision int64) (domain.Mission, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.checked(id, revision)
	if err != nil {
		return m, err
	}
	if e.active != nil {
		return m, errors.New("another operation is active")
	}
	if m.State != "awaiting_approval" || m.Plan == nil {
		return m, errors.New("mission is not awaiting approval")
	}
	if err = e.preflightModelRoles(ctx, m, domain.Worker, domain.Debugger, domain.QA, domain.Auditor); err != nil {
		return m, err
	}
	if err = e.checkSnapshot(m); err != nil {
		return m, err
	}
	seal, err := authority(m, filepath.Join(e.dir(id), "base"), true)
	if err != nil {
		return m, err
	}
	if !hasSeal(m, "plan_proposal", seal) {
		return m, errors.New("plan approval inputs changed")
	}
	image, err := e.runner.Resolve(ctx, m.Spec.Image)
	if err != nil {
		return m, err
	}
	if image != m.Spec.ImageID {
		return m, errors.New("image changed since plan; re-create mission with intended image")
	}
	candidate, err := e.selectedCandidate(m)
	if err != nil {
		return m, err
	}
	digest, err := workspace.Digest(candidate)
	if err != nil {
		return m, err
	}
	if digest != m.CandidateDigest {
		return m, errors.New("candidate changed before approval")
	}
	m, err = e.s.ApproveMissionExecution(id, revision, seal)
	if err != nil {
		return m, err
	}
	e.start(m, e.execution)
	return m, nil
}
