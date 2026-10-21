package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type failurePlanner struct {
	candidate string
	calls     int
}

type recoveryFailurePlanner struct {
	candidate domain.GenerationRef
	code      string
	calls     int
}

func (g *recoveryFailurePlanner) Complete(_ context.Context, r model.Request) (model.Result, error) {
	g.calls++
	var input struct {
		Bundle *domain.FailureBundle `json:"replan_context"`
	}
	if err := json.Unmarshal([]byte(r.Prompt), &input); err != nil {
		return model.Result{}, err
	}
	b := input.Bundle
	if r.Role != domain.Planner || b == nil || b.Digest == "" || b.CandidateGeneration != g.candidate || len(b.Failures) != 1 || b.Failures[0].Code != g.code {
		return model.Result{}, errors.New("missing exact typed recovery failure")
	}
	var proposal map[string]any
	_ = json.Unmarshal([]byte(plan), &proposal)
	proposal["adjustments"] = []domain.PlanAdjustment{{FailureID: b.Failures[0].ID, StepIDs: []string{"one"}, Reason: "explicitly generate and verify a fresh candidate"}}
	raw, _ := json.Marshal(proposal)
	return model.Result{Text: string(raw), UsageKnown: true}, nil
}

// These regressions must fail if freeze loses linked infrastructure evidence or
// treats a missing terminal After as proof that no local selection committed.
func TestCodingRecoveryFailuresCanExplicitlyReplan(t *testing.T) {
	for _, kind := range []string{"infrastructure", "committed_selection"} {
		t.Run(kind, func(t *testing.T) {
			e, s, g, r, m := codingSetup(t, plan, edit)
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			base := *m.CandidateGeneration
			code := "infrastructure_failed"
			db := failureDB(t, e)
			if kind == "infrastructure" {
				e.runner = r.fakeRunner
				r.runErr = errors.New("local fixture infrastructure unavailable")
			} else {
				code = "interrupted"
				if _, err := db.Exec(`CREATE TRIGGER replan_finish_fault BEFORE INSERT ON events WHEN NEW.kind='coding.finished' BEGIN SELECT RAISE(ABORT,'local fixture write failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			if kind == "committed_selection" {
				if _, err := db.Exec(`DROP TRIGGER replan_finish_fault`); err != nil {
					t.Fatal(err)
				}
				e = New(s, e.gateway, Options{DataDir: e.options.DataDir, Runner: r})
				for i := 0; i < 2; i++ {
					if err := e.Recover(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
			}
			m, _ = s.GetMission(m.ID)
			last := m.CodingAttempts[len(m.CodingAttempts)-1]
			if m.State != "blocked" || last.FailureCode != code || len(g.roles) != 2 || r.calls != 0 {
				t.Fatalf("failure dispatched automatic repair or recovery replay: %+v", m)
			}
			if kind == "infrastructure" && (len(m.Evidence) != 1 || last.VerificationID != m.Evidence[0].ID || m.Evidence[0].Passed) {
				t.Fatal("infrastructure evidence was not linked")
			}
			if kind == "committed_selection" && (last.After != nil || *m.CandidateGeneration == base) {
				t.Fatal("fixture did not preserve interrupted committed selection")
			}
			r.runErr = nil
			before := m
			planner := &recoveryFailurePlanner{candidate: *m.CandidateGeneration, code: code}
			e.gateway = planner
			e.runner = r
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatalf("explicit replan of complete typed recovery material: %v", err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "awaiting_approval" || planner.calls != 1 || r.calls != 0 {
				t.Fatalf("explicit replan did not stop for independent approval: %s %s", m.State, m.Error)
			}
			if m.Budget.UsedCalls != before.Budget.UsedCalls+1 || m.CandidateDigest != base.Digest || m.CandidateGeneration.ID == base.ID || len(m.RunArchives) != 1 || !reflect.DeepEqual(m.CodingAttempts, before.CodingAttempts) || len(m.Evidence) != 0 || len(m.Reviews) != 0 || m.Delivery != nil {
				t.Fatal("replan lost history/budget, rewrote terminal trace, or resumed candidate")
			}
			if !reflect.DeepEqual(m.RunArchives[0].CodingAttempts, before.CodingAttempts) || m.RunArchives[0].CandidateDigest != before.CandidateDigest {
				t.Fatal("archive lost original failure facts")
			}
			if _, err := e.candidate(m); err == nil {
				t.Fatal("old execution approval authorized the new failure-bound proposal")
			}
			if kind == "committed_selection" && m.ReplanContext.Failures[0].CommittedOperationID != before.CandidateGeneration.ID {
				t.Fatal("planner projection omitted the known committed selection")
			}
			e.gateway = &codingReviewGateway{script: &scripted{outputs: []string{edit, codingPass, codingPass}}}
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "awaiting_delivery" || r.calls != 1 {
				t.Fatalf("fresh approved execution failed: %s %s", m.State, m.Error)
			}
		})
	}
}

func recoveredCodingSelection(t *testing.T) (*Engine, *scripted, *codingContentRunner, domain.Mission) {
	t.Helper()
	e, s, g, r, m := codingSetup(t, plan, edit)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	db := failureDB(t, e)
	if _, err := db.Exec(`CREATE TRIGGER selection_finish_fault BEFORE INSERT ON events WHEN NEW.kind='coding.finished' BEGIN SELECT RAISE(ABORT,'local fixture write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	if _, err := db.Exec(`DROP TRIGGER selection_finish_fault`); err != nil {
		t.Fatal(err)
	}
	e = New(s, e.gateway, Options{DataDir: e.options.DataDir, Runner: r})
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if len(m.CodingAttempts) != 1 || m.CodingAttempts[0].Status != "interrupted" || m.CodingAttempts[0].After != nil || *m.CandidateGeneration == *m.CodingAttempts[0].Before {
		t.Fatal("invalid committed-selection fixture")
	}
	return e, g, r, m
}

func TestCodingRecoveredSelectionRejectsMismatchedLocalFactsBeforePlanning(t *testing.T) {
	for _, kind := range []string{"uncommitted", "foreign", "epoch", "plan", "before", "result", "changes", "approval", "base", "early", "late", "candidate_tamper", "parent_tamper"} {
		t.Run(kind, func(t *testing.T) {
			e, g, r, m := recoveredCodingSelection(t)
			op, err := e.s.GetLocalOperation(m.CandidateGeneration.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "uncommitted":
				op.Status = "prepared"
			case "foreign":
				op.MissionID = "foreign"
			case "epoch":
				op.Epoch++
			case "plan":
				op.PlanRevision++
			case "before":
				op.Before = *m.CandidateGeneration
			case "result":
				op.Result.Generation = &op.Before
			case "changes":
				op.Result.Changes = nil
			case "approval":
				op.ApprovalDigest = "foreign"
			case "base":
				op.BaseDigest = "foreign"
			case "early":
				started, _ := time.Parse(time.RFC3339Nano, m.CodingAttempts[0].StartedAt)
				op.CreatedAt = started.Add(-time.Second).Format(time.RFC3339Nano)
			case "late":
				finished, _ := time.Parse(time.RFC3339Nano, m.CodingAttempts[0].FinishedAt)
				op.UpdatedAt = finished.Add(time.Second).Format(time.RFC3339Nano)
			case "candidate_tamper":
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "generations", m.CandidateGeneration.ID, "tree", "src", "main.txt"), []byte("tampered\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "parent_tamper":
				path := filepath.Join(e.dir(m.ID), "generations", m.CandidateGeneration.ID, "manifest.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var manifest map[string]any
				if err := json.Unmarshal(raw, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest["parent"] = nil
				raw, _ = json.Marshal(manifest)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			result, _ := json.Marshal(op.Result)
			intent := op
			intent.Result = nil
			raw, _ := json.Marshal(intent)
			// Simulate disk corruption below the immutable SQL/API boundary.
			db := failureDB(t, e)
			if _, err := db.Exec(`DROP TRIGGER immutable_operation_intent; DROP TRIGGER operation_transition`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE local_operations SET intent=?,status=?,result=?,updated_at=? WHERE id=?`, raw, op.Status, result, op.UpdatedAt, op.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err == nil {
				finishCoding(e)
				t.Fatal("invalid recovered selection reached Planner")
			}
			got, _ := e.s.GetMission(m.ID)
			if got.Revision != m.Revision || got.Budget != m.Budget || !reflect.DeepEqual(got.CodingAttempts, m.CodingAttempts) || len(g.roles) != 2 || r.calls != 0 {
				t.Fatal("rejected recovery mutated or replayed")
			}
		})
	}
}

func TestCodingRecoveredSelectionRemainsBoundAfterFreeze(t *testing.T) {
	for _, kind := range []string{"archive", "operation", "generation"} {
		t.Run(kind, func(t *testing.T) {
			e, _, r, m := recoveredCodingSelection(t)
			e.gateway = &recoveryFailurePlanner{candidate: *m.CandidateGeneration, code: "interrupted"}
			if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = e.s.GetMission(m.ID)
			if m.State != "awaiting_approval" {
				t.Fatal(m.Error)
			}
			op := m.RunArchives[0].RecoveredSelection
			if op == nil {
				t.Fatal("missing separate committed selection fact")
			}
			switch kind {
			case "archive":
				if _, err := e.s.UpdateMission(m.ID, m.Revision, "fixture.tamper", func(next *domain.Mission) error { next.RunArchives[0].RecoveredSelection = nil; return nil }); err == nil {
					t.Fatal("archive selection fact removable")
				}
				return
			case "operation":
				db := failureDB(t, e)
				if _, err := db.Exec(`DROP TRIGGER operation_transition`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE local_operations SET status='prepared' WHERE id=?`, op.ID); err != nil {
					t.Fatal(err)
				}
			case "generation":
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "generations", op.ID, "tree", "src", "main.txt"), []byte("tampered\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
				finishCoding(e)
				t.Fatal("changed frozen recovery material approved")
			}
			got, _ := e.s.GetMission(m.ID)
			if got.Revision != m.Revision || got.Budget != m.Budget || r.calls != 0 {
				t.Fatal("tampered recovery context dispatched")
			}
		})
	}
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
	e.gateway = &codingReviewGateway{script: &scripted{outputs: []string{edit, codingPass, codingPass}}}
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
