package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
)

const codingReplanGoal = "SYNTHETIC C2_REPLAN: exhaust local content checks, explicitly replan, then correct src/main.txt"

// Only the owned local acceptance runtime installs this gateway. Store reads
// validate the supplied fixture inputs; no provider or account is accessed.
type codingReplanGateway struct {
	fallback codingLoopGateway
	store    *store.Store
}

func (g codingReplanGateway) Complete(ctx context.Context, req model.Request) (model.Result, error) {
	var route struct {
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal([]byte(req.Prompt), &route); err != nil {
		return model.Result{}, err
	}
	if !strings.HasPrefix(route.Goal, "SYNTHETIC C2_REPLAN:") {
		return g.fallback.Complete(ctx, req)
	}
	var input struct {
		Goal     string `json:"goal"`
		Identity struct {
			MissionID string               `json:"mission_id"`
			Epoch     int64                `json:"epoch"`
			Candidate domain.GenerationRef `json:"candidate_generation"`
		} `json:"coding_identity"`
		Failure *domain.FailureBundle       `json:"replan_context"`
		Extra   domain.CodingReviewContract `json:"extra"`
	}
	// Worker/Debugger extra is a step/repair object, not a review contract.
	if err := json.Unmarshal([]byte(req.Prompt), &input); err != nil {
		return model.Result{}, err
	}
	if g.store == nil {
		return model.Result{}, errors.New("C2 fixture requires owned local store")
	}
	m, err := g.store.GetMission(input.Identity.MissionID)
	if err != nil {
		return model.Result{}, err
	}
	if m.CodingVersion != 1 || m.ToolMode != "" || m.Goal != input.Goal || m.Epoch != input.Identity.Epoch || m.CandidateGeneration == nil || *m.CandidateGeneration != input.Identity.Candidate || !reflect.DeepEqual(m.ReplanContext, input.Failure) {
		return model.Result{}, errors.New("C2 fixture requires matching task, candidate and sealed failure inputs")
	}
	if err := domain.ValidateReplanContext(m); err != nil {
		return model.Result{}, err
	}
	if input.Failure != nil && len(input.Failure.Failures) == 0 {
		return model.Result{}, errors.New("C2 fixture requires blocking failures")
	}
	var output any
	switch req.Role {
	case domain.Planner:
		proposal := map[string]any{"summary": "SYNTHETIC C2 initial proposal; separate execution approval required", "steps": []any{map[string]any{"id": "repair", "title": "Correct the disposable source", "description": "Change only src/main.txt; frozen content-check simulation is not command execution", "files": []string{"src/main.txt"}, "depends_on": []string{}}}}
		if input.Failure != nil {
			adjustments := []domain.PlanAdjustment{}
			for _, failure := range input.Failure.Failures {
				if failure.ID == "" || failure.SourceDigest == "" {
					return model.Result{}, errors.New("missing sealed failure references")
				}
				adjustments = append(adjustments, domain.PlanAdjustment{FailureID: failure.ID, StepIDs: []string{"repair"}, Reason: "Replace the unchanged bad src/main.txt with repaired content, then rerun the frozen content-check simulation", Unresolved: failure.Code == "outcome_unknown"})
			}
			proposal["summary"], proposal["adjustments"] = "SYNTHETIC C2 failure-backed targeted correction; fresh user approval required", adjustments
		}
		output = proposal
	case domain.Worker, domain.Debugger:
		content := "still-wrong\n"
		if input.Failure != nil {
			content = "repaired\n"
		}
		output = map[string]any{"summary": "SYNTHETIC C2 local candidate proposal", "edits": []any{map[string]any{"path": "src/main.txt", "content": content}}}
	case domain.QA, domain.Auditor:
		contract, err := domain.NewCodingReviewContract(m, req.Role)
		if err != nil || !reflect.DeepEqual(input.Extra, contract) {
			return model.Result{}, errors.New("C2 fixture requires exact current controller review references")
		}
		checks := []domain.CodingReviewCheck{}
		passed := true
		for _, ref := range input.Extra.Required {
			checks = append(checks, domain.CodingReviewCheck{ID: ref.ID, Passed: &passed, Summary: "SYNTHETIC assessment of supplied controller reference; no command executed by model", VerificationID: ref.VerificationID, AcceptanceIndex: ref.AcceptanceIndex, ReferenceDigest: ref.ReferenceDigest, Paths: []string{}})
		}
		output = map[string]any{"passed": true, "summary": "SYNTHETIC role-specific evidence review (not test execution)", "findings": []any{}, "binding_digest": input.Extra.BindingDigest, "checks": checks}
	default:
		return g.fallback.Complete(ctx, req)
	}
	raw, err := json.Marshal(output)
	return model.Result{Text: string(raw), UsageKnown: true}, err
}

func (g codingReplanGateway) PreflightModelSelection(ctx context.Context, schema int, selection domain.RoleModelSelection) error {
	return g.fallback.PreflightModelSelection(ctx, schema, selection)
}
func (g codingReplanGateway) PreflightAccountProfile(ctx context.Context, profile domain.Profile) error {
	return g.fallback.PreflightAccountProfile(ctx, profile)
}

func codingReplanCreateRequest(repo string) engine.CreateRequest {
	req := codingLoopCreateRequest(repo, "default")
	req.Title, req.Goal, req.CodingVersion = "SYNTHETIC C2 explicit failure replan", codingReplanGoal, 1
	return req
}

func seedCodingReplanScenario(r *acceptanceRuntime) error {
	m, err := r.engine.Create(context.Background(), codingReplanCreateRequest(r.Repo))
	if err == nil {
		r.scenarios["coding_loop_replan"] = m.ID
	}
	return err
}
