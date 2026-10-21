package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"testing"
)

type failurePlanner struct {
	candidate string
	calls     int
}

func (g *failurePlanner) Complete(_ context.Context, r model.Request) (model.Result, error) {
	g.calls++
	var input struct {
		ReplanContext struct {
			Digest              string
			ArchiveDigest       string               `json:"archive_digest"`
			CandidateGeneration domain.GenerationRef `json:"candidate_generation"`
			Failures            []struct {
				ID           string
				SourceDigest string `json:"source_digest"`
			}
		} `json:"replan_context"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return model.Result{}, err
	}
	b := input.ReplanContext
	if r.Role != domain.Planner || b.Digest == "" || b.ArchiveDigest == "" || b.CandidateGeneration.ID != g.candidate || len(b.Failures) != 3 {
		return model.Result{}, errors.New("missing matching sealed failure material")
	}
	var proposal map[string]any
	_ = json.Unmarshal([]byte(plan), &proposal)
	adjustments := []map[string]any{}
	for _, f := range b.Failures {
		if f.ID == "" || f.SourceDigest == "" {
			return model.Result{}, errors.New("missing failure source")
		}
		adjustments = append(adjustments, map[string]any{"failure_id": f.ID, "step_ids": []string{"one"}, "reason": "correct the candidate content", "unresolved": false})
	}
	proposal["adjustments"] = adjustments
	raw, _ := json.Marshal(proposal)
	return model.Result{Text: string(raw), UsageKnown: true}, nil
}

func failedCodingRun(t *testing.T) (*Engine, domain.Mission) {
	t.Helper()
	bad := `{"summary":"unchanged","edits":[{"path":"src/main.txt","content":"before\n"}]}`
	e, s, g, r, m := codingSetup(t, plan, bad, bad, bad)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || len(g.roles) != 4 || r.calls != 3 {
		t.Fatal("fixture failed", m.State)
	}
	return e, m
}

func TestCodingExplicitReplanSealsFailuresAndRequiresApproval(t *testing.T) {
	e, failed := failedCodingRun(t)
	g := &failurePlanner{candidate: failed.CandidateGeneration.ID}
	e.gateway = g
	beforeCalls := failed.Budget.UsedCalls
	frozenBaseDigest := failed.CodingAttempts[0].Before.Digest
	if _, err := e.Plan(context.Background(), failed.ID, failed.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	replanned, _ := e.s.GetMission(failed.ID)
	if replanned.State != "awaiting_approval" {
		t.Fatalf("sealed failures did not reach planner: %s %s", replanned.State, replanned.Error)
	}
	if replanned.Budget.UsedCalls <= beforeCalls {
		t.Fatal("planner was not accounted")
	}
	if replanned.CandidateDigest != frozenBaseDigest {
		t.Fatal("replan resumed failed candidate")
	}
	if len(replanned.Evidence) != 0 || len(replanned.Reviews) != 0 {
		t.Fatal("old delivery evidence leaked into new run")
	}
	if len(replanned.RunArchives) != 1 || g.calls != 1 || len(replanned.CodingAttempts) != 6 {
		t.Fatal("replan executed or lost history")
	}
	e.gateway = &scripted{outputs: []string{edit, review, review}}
	if _, err := e.Approve(context.Background(), replanned.ID, replanned.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	delivered, _ := e.s.GetMission(failed.ID)
	if delivered.State != "awaiting_delivery" {
		t.Fatalf("corrected execution failed: %s %s", delivered.State, delivered.Error)
	}
}

func TestCodingReplanRejectsMutableBundleAtStoreBoundary(t *testing.T) {
	e, m := failedCodingRun(t)
	e.gateway = &failurePlanner{candidate: m.CandidateGeneration.ID}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	if _, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.tamper", func(m *domain.Mission) error { m.ReplanContext = nil; return nil }); err == nil {
		t.Fatal("store allowed sealed failure removal")
	}
}

func TestCodingReplanRevisionMappingsAndUnresolvedBlockApproval(t *testing.T) {
	e, m := failedCodingRun(t)
	e.gateway = &failurePlanner{candidate: m.CandidateGeneration.ID}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = e.s.GetMission(m.ID)
	var proposed EditablePlan
	raw, _ := json.Marshal(m.Plan)
	_ = json.Unmarshal(raw, &proposed)
	proposed.Steps[0].ID = "new"
	req := PlanRevisionRequest{ExpectedMissionRevision: m.Revision, ExpectedPlanRevision: m.Plan.Revision, Reason: "change", Plan: proposed}
	if _, _, err := e.RevisePlan(context.Background(), m.ID, req); err == nil {
		t.Fatal("removed mapped step accepted")
	}
	proposed.Steps[0].ID = "one"
	proposed.Adjustments[0].Unresolved = true
	proposed.Adjustments[0].StepIDs = nil
	req.Plan = proposed
	revised, _, err := e.RevisePlan(context.Background(), m.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Approve(context.Background(), m.ID, revised.Revision); err == nil {
		t.Fatal("unresolved failure approved")
	}
	proposalSeal := revised.Approvals[len(revised.Approvals)-1].Digest
	if _, err = e.s.ApproveMissionExecution(m.ID, revised.Revision, proposalSeal); err == nil {
		t.Fatal("store approved unresolved failure")
	}
	got, _ := e.s.GetMission(m.ID)
	if got.State != "awaiting_approval" || got.Budget != m.Budget {
		t.Fatal("revision executed")
	}
}

func TestCodingInitialPlannerKeepsLegacyOutputSchema(t *testing.T) {
	var proposal map[string]any
	_ = json.Unmarshal([]byte(plan), &proposal)
	proposal["adjustments"] = []any{}
	raw, _ := json.Marshal(proposal)
	if err := decodeStrict(string(raw), &planOutput{}); err == nil {
		t.Fatal("legacy reusable planner schema broadened")
	}
	e, s, _, _, m := codingSetup(t, string(raw))
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	got, _ := s.GetMission(m.ID)
	if got.State != "blocked" {
		t.Fatal("non-replan schema broadened")
	}
}
