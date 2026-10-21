package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"foundry/internal/domain"
	"foundry/internal/store"
)

// EditablePlan deliberately excludes runtime status, revisions and authority.
type EditablePlan struct {
	Adjustments []domain.PlanAdjustment `json:"adjustments,omitempty"`
	Summary     string                  `json:"summary"`
	Steps       []EditableStep          `json:"steps"`
}

type EditableStep struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
	Files       []string `json:"files"`
}

type PlanRevisionRequest struct {
	ExpectedMissionRevision int64        `json:"expected_mission_revision"`
	ExpectedPlanRevision    int64        `json:"expected_plan_revision"`
	Reason                  string       `json:"reason"`
	Plan                    EditablePlan `json:"plan"`
}

// ErrInvalidPlanRevision distinguishes this command's input/state refusal from
// stale CAS and storage failures without changing other command error contracts.
var ErrInvalidPlanRevision = errors.New("invalid plan revision")

// ErrPlanRevisionInput identifies correctable editable input, not preflight or
// lifecycle failures. HTTP callers use only this category, never raw details.
var ErrPlanRevisionInput = errors.New("invalid plan revision input")

func invalidRevision(err error) error { return fmt.Errorf("%w: %v", ErrInvalidPlanRevision, err) }
func invalidRevisionInput(err error) error {
	return fmt.Errorf("%w: %w: %v", ErrInvalidPlanRevision, ErrPlanRevisionInput, err)
}

func (p EditablePlan) pendingPlan() domain.Plan {
	plan := domain.Plan{Summary: p.Summary, Steps: make([]domain.Step, 0, len(p.Steps)), Adjustments: p.Adjustments}
	for _, step := range p.Steps {
		plan.Steps = append(plan.Steps, domain.Step{ID: step.ID, Title: step.Title, Description: step.Description, DependsOn: step.DependsOn, Files: step.Files, Status: "pending"})
	}
	return plan
}

func comparablePlan(plan domain.Plan) string {
	plan.Revision = 0
	plan.Steps = append([]domain.Step(nil), plan.Steps...)
	for i := range plan.Steps {
		plan.Steps[i].Status = ""
		if len(plan.Steps[i].DependsOn) == 0 {
			plan.Steps[i].DependsOn = nil
		}
	}
	raw, _ := json.Marshal(plan)
	return string(raw)
}

// RevisePlan saves a new proposal only. Filesystem checks and authority hashing
// happen before the atomic store callback, which appends exactly one seal.
func (e *Engine) RevisePlan(ctx context.Context, id string, request PlanRevisionRequest) (domain.Mission, domain.PlanRevision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	empty := domain.PlanRevision{}
	m, err := e.checked(id, request.ExpectedMissionRevision)
	if err != nil {
		if !errors.Is(err, store.ErrConflict) && m.ID != "" && supportedMode(m.WorkspaceMode) != nil {
			err = invalidRevision(err)
		}
		return m, empty, err
	}
	// Both CAS checks precede filesystem reads and expensive validation.
	if m.Plan != nil && m.Plan.Revision != request.ExpectedPlanRevision {
		return m, empty, store.ErrConflict
	}
	if e.active != nil || m.State != "awaiting_approval" || m.Plan == nil {
		return m, empty, invalidRevision(errors.New("mission must await approval with no active operation"))
	}
	operations, err := e.s.ListLocalOperations(id)
	if err != nil {
		return m, empty, err
	}
	for _, op := range operations {
		if op.Status == "pending" || op.Status == "prepared" {
			return m, empty, invalidRevision(errors.New("unfinished local operation"))
		}
	}
	if err = ctx.Err(); err != nil {
		return m, empty, err
	}
	if err = e.checkSnapshot(m); err != nil {
		return m, empty, invalidRevision(err)
	}
	if _, err = e.selectedCandidate(m); err != nil {
		return m, empty, invalidRevision(err)
	}
	base := filepath.Join(e.dir(id), "base")
	seal, err := authority(m, base, true)
	if err != nil {
		return m, empty, invalidRevision(err)
	}
	if !hasSeal(m, "plan_proposal", seal) {
		return m, empty, invalidRevision(errors.New("current proposal inputs changed"))
	}
	if err = boundedPlanText("reason", request.Reason, 1000); err != nil {
		return m, empty, invalidRevisionInput(err)
	}
	next := request.Plan.pendingPlan()
	if err = domain.ValidatePlanAdjustments(m, next); err != nil {
		return m, empty, invalidRevisionInput(err)
	}
	if err = validateExecutablePlan(next, m.Spec); err != nil {
		return m, empty, invalidRevisionInput(err)
	}
	if comparablePlan(*m.Plan) == comparablePlan(next) {
		return m, empty, invalidRevisionInput(errors.New("plan revision must change the plan"))
	}
	next.Revision = m.Plan.Revision + 1
	if next.Revision <= m.Plan.Revision {
		return m, empty, invalidRevision(errors.New("plan revision overflow"))
	}
	proposed := m
	proposed.Plan = &next
	seal, err = authority(proposed, base, true)
	if err != nil {
		return m, empty, invalidRevision(err)
	}
	return e.s.RevisePlan(id, request.ExpectedMissionRevision, request.ExpectedPlanRevision, request.Reason, next, func(current *domain.Mission) error {
		current.Approvals = append(current.Approvals, domain.Approval{Kind: "plan_proposal", Digest: seal, Revision: next.Revision, PlanRevision: next.Revision})
		return nil
	})
}
