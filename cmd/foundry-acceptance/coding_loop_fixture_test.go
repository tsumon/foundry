package main

import (
	"context"
	"errors"
	"foundry/internal/testutil"
	"os"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
)

func codingLoopRequest(runtime *acceptanceRuntime, mode string) engine.CreateRequest {
	return codingLoopCreateRequest(runtime.Repo, mode)
}

func runCodingLoopToApproval(t *testing.T, runtime *acceptanceRuntime, mission domain.Mission) domain.Mission {
	t.Helper()
	if _, err := runtime.engine.PlanControlled(context.Background(), mission.ID, mission.Revision, true); err != nil {
		t.Fatal(err)
	}
	mission, err := waitSyntheticMission(runtime.store, mission.ID, "awaiting_approval", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if mission.State != "awaiting_approval" {
		t.Fatalf("coding loop plan blocked: %s", mission.Error)
	}
	return mission
}

func TestCodingLoopDebuggerRequiresLatestFailedContentEvidence(t *testing.T) {
	gateway := codingLoopGateway{}
	valid := `{"goal":"SYNTHETIC C1_CONTENT_REPAIR default: repair","changes":[{"path":"src/main.txt","before":"SYNTHETIC source before\n","after":"still-wrong\n"}],"evidence":[{"id":"failure-one","candidate_digest":"` + strings.Repeat("a", 64) + `","image_id":"sha256:synthetic-coding-loop-content-fixture","created_at":"2026-10-21T00:00:00Z","acceptance_revision":1,"results":[{"command":"test -f /workspace/src/main.txt","output":"SIMULATED content check; no command executed","exit_code":1,"timed_out":false}],"passed":false}]}`
	for name, prompt := range map[string]string{
		"missing evidence": `{"goal":"SYNTHETIC C1_CONTENT_REPAIR default: repair","changes":[{"path":"src/main.txt","after":"still-wrong\n"}],"evidence":[]}`,
		"passed evidence":  strings.Replace(valid, `"passed":false`, `"passed":true`, 1),
		"wrong command":    strings.Replace(valid, codingLoopCommand, "true", 1),
		"wrong output":     strings.Replace(valid, codingLoopOutput, "model claimed success", 1),
		"zero exit":        strings.Replace(valid, `"exit_code":1`, `"exit_code":0`, 1),
		"wrong candidate":  strings.Replace(valid, `"after":"still-wrong\n"`, `"after":"other\n"`, 1),
		"bad digest":       strings.Replace(valid, strings.Repeat("a", 64), "not-a-digest", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := gateway.Complete(context.Background(), model.Request{Role: domain.Debugger, Prompt: prompt}); err == nil {
				t.Fatal("debugger repaired without the controller's latest failed content evidence")
			}
		})
	}
	result, err := gateway.Complete(context.Background(), model.Request{Role: domain.Debugger, Prompt: valid})
	if err != nil || !strings.Contains(result.Text, `"content":"repaired\n"`) {
		t.Fatalf("valid failed content evidence did not produce repair: %v %s", err, result.Text)
	}
}

func TestCodingLoopRepairScenarioUsesContentAndIndependentApprovals(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	id := runtime.scenarios["coding_loop_repair"]
	mission, err := runtime.store.GetMission(id)
	if err != nil {
		t.Fatal(err)
	}
	if mission.State != "draft" || mission.WorkspaceMode != domain.WorkspaceModeGenerationsV1 || mission.Plan != nil {
		t.Fatalf("scenario must begin at context approval with explicit recovery mode: %+v", mission)
	}
	mission = runCodingLoopToApproval(t, runtime, mission)
	request := engine.PlanRevisionRequest{
		ExpectedMissionRevision: mission.Revision,
		ExpectedPlanRevision:    mission.Plan.Revision,
		Reason:                  "Human narrows the synthetic repair before execution",
		Plan: engine.EditablePlan{
			Summary: "Human-revised content repair",
			Steps: []engine.EditableStep{{
				ID: "repair", Title: "Repair the fixture", Description: "Change only src/main.txt to the required repaired content", Files: []string{"src/main.txt"},
			}},
		},
	}
	mission, revision, err := runtime.engine.RevisePlan(context.Background(), mission.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Source != "user" || mission.Plan.Summary != request.Plan.Summary {
		t.Fatalf("user plan revision not preserved: %+v %+v", mission.Plan, revision)
	}
	if _, err = runtime.engine.Approve(context.Background(), mission.ID, mission.Revision-1); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale execution approval accepted: %v", err)
	}
	if _, err = runtime.engine.Approve(context.Background(), mission.ID, mission.Revision); err != nil {
		t.Fatal(err)
	}
	mission, err = waitSyntheticMission(runtime.store, mission.ID, "awaiting_delivery", "blocked")
	if err != nil {
		t.Fatal(err)
	}
	if mission.State != "awaiting_delivery" || len(mission.Evidence) != 2 || mission.Evidence[0].Passed || !mission.Evidence[1].Passed || len(mission.Reviews) != 2 || !mission.Reviews[0].Passed || !mission.Reviews[1].Passed {
		t.Fatalf("repair did not traverse failure, debugger and both reviews: %+v", mission)
	}
	for _, evidence := range mission.Evidence {
		if evidence.SourceVersion != 1 || evidence.SourceKind != "synthetic_content_check" {
			t.Fatalf("content check lacks trusted simulation source: %+v", evidence)
		}
		if len(evidence.Results) != 1 || evidence.Results[0].Output != "SIMULATED content check; no command executed" {
			t.Fatalf("fixture claimed a real command result: %+v", evidence.Results)
		}
	}
	for _, approval := range mission.Approvals {
		if approval.Kind == "deliver" {
			t.Fatal("scenario creation or execution approved delivery")
		}
	}
	delivered, err := runtime.engine.Deliver(context.Background(), mission.ID, mission.Revision)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile(delivered.Delivery.PatchPath)
	if err != nil || !strings.Contains(string(patch), "+repaired") {
		t.Fatalf("exported patch lacks repaired content: %v %s", err, patch)
	}
	report, err := os.ReadFile(delivered.Delivery.ReportPath)
	if err != nil || !strings.Contains(string(report), "内容检查模拟（未执行命令）") || !strings.Contains(string(report), "模拟验证未通过") || !strings.Contains(string(report), "模拟验证通过") {
		t.Fatalf("report lost explicit simulation outcomes: %v %s", err, report)
	}
	check := testutil.Git(t, "apply", "--check", delivered.Delivery.PatchPath)
	check.Dir = runtime.Repo
	if output, checkErr := check.CombinedOutput(); checkErr != nil {
		t.Fatalf("exported patch does not apply to the frozen source: %v %s", checkErr, output)
	}
	source, err := os.ReadFile(runtime.Repo + "/src/main.txt")
	if err != nil || string(source) != "SYNTHETIC source before\n" {
		t.Fatalf("source repository changed: %v %q", err, source)
	}
}

func TestCodingLoopNegativeModelResultsNeverDeliverOrReplay(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	for _, tc := range []struct {
		mode          string
		wantEvidence  int
		wantReviews   int
		wantUsedCalls int
	}{
		{mode: "same_debugger", wantEvidence: 3, wantUsedCalls: 4},
		{mode: "qa_reject", wantEvidence: 2, wantReviews: 1, wantUsedCalls: 4},
		{mode: "auditor_reject", wantEvidence: 2, wantReviews: 2, wantUsedCalls: 5},
		{mode: "unknown_output", wantUsedCalls: 2},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			mission, err := runtime.engine.Create(context.Background(), codingLoopRequest(runtime, tc.mode))
			if err != nil {
				t.Fatal(err)
			}
			mission = runCodingLoopToApproval(t, runtime, mission)
			if _, err = runtime.engine.Approve(context.Background(), mission.ID, mission.Revision); err != nil {
				t.Fatal(err)
			}
			mission, err = waitSyntheticMission(runtime.store, mission.ID, "blocked", "awaiting_delivery")
			if err != nil {
				t.Fatal(err)
			}
			if mission.State != "blocked" || mission.Delivery != nil || len(mission.Evidence) != tc.wantEvidence || len(mission.Reviews) != tc.wantReviews || mission.Budget.UsedCalls != tc.wantUsedCalls {
				t.Fatalf("negative result acquired delivery authority: %+v", mission)
			}
			before := mission.Budget.UsedCalls
			if err = runtime.engine.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			mission, err = runtime.store.GetMission(mission.ID)
			if err != nil || mission.Budget.UsedCalls != before || mission.Delivery != nil {
				t.Fatalf("recovery replayed negative result: %+v %v", mission, err)
			}
			if _, err = runtime.engine.Deliver(context.Background(), mission.ID, mission.Revision); err == nil {
				t.Fatal("blocked negative result delivered")
			}
		})
	}
}
