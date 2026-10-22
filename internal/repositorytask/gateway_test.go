package repositorytask

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"testing"
)

func gatewayMission(t *testing.T) (domain.Mission, map[string]string) {
	t.Helper()
	d, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	spec := d.ImportSpec()
	p := domain.Profile{ID: "local", Kind: "synthetic", Model: "SCRIPTED", Protocol: "chat_completions", BaseURL: "https://synthetic.invalid"}
	m := domain.Mission{ID: "mission", Goal: d.Goal, Epoch: 1, CodingVersion: 1, ImportVersion: 1, WorkspaceMode: "generations_v1", State: "planning", Spec: domain.Spec{Scope: spec.Scope, Protected: spec.Protected, Acceptance: spec.Acceptance, Image: spec.Image, MaxCalls: 16, C4: &domain.C4Profile{Mode: "synthetic"}}, CandidateGeneration: &domain.GenerationRef{ID: "generation", Digest: "digest"}, CandidateDigest: "digest", Profiles: map[domain.Role]domain.Profile{}, Approvals: []domain.Approval{{Kind: "context", Digest: "context-seal", Revision: 1}}, RepositoryImport: &domain.RepositoryImportBinding{BaseDigest: "base"}}
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		m.Profiles[role] = p
	}
	m.RepositoryImport.ImportSpecDigest = domain.RepositoryImportSpecDigest(spec)
	files := map[string]string{Descriptor: example, "internal/label/label.go": "original", "internal/label/display.go": "old"}
	for p, c := range files {
		m.ContextManifest = append(m.ContextManifest, domain.FileMeta{Path: p, Bytes: int64(len(c)), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(c)))})
	}
	return m, files
}

func gatewayRequest(m domain.Mission, files map[string]string, role domain.Role, extra any) model.Request {
	raw, _ := json.Marshal(map[string]any{"coding_identity": map[string]any{"mission_id": m.ID, "epoch": m.Epoch, "candidate_generation": m.CandidateGeneration}, "goal": m.Goal, "spec": m.Spec, "context": files, "plan": m.Plan, "changes": m.Changes, "evidence": m.Evidence, "replan_context": m.ReplanContext, "extra": extra})
	return model.Request{Role: role, Profile: m.Profiles[role], Prompt: string(raw)}
}

func TestGatewayPlannerAndCurrentWorker(t *testing.T) {
	m, files := gatewayMission(t)
	result, err := complete(m, gatewayRequest(m, files, domain.Planner, nil))
	if err != nil {
		t.Fatal(err)
	}
	var plan domain.Plan
	if err = json.Unmarshal([]byte(result.Text), &plan); err != nil || len(plan.Steps) != 1 || len(plan.Steps[0].Files) != 2 {
		t.Fatal(result, err)
	}
	m.Plan = &plan
	m.Plan.Revision = 3
	m.State = "running"
	// User regrouping/renaming uses files, not descriptor step IDs.
	m.Plan.Steps[0].ID = "renamed"
	m.Plan.Steps[0].Files = []string{"internal/label/label.go"}
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: "execute-seal", PlanRevision: 3})
	m.CodingAttempts = []domain.CodingAttempt{{Role: domain.Worker, Phase: "worker", StepID: "renamed", Status: "running", Epoch: m.Epoch, PlanRevision: 3, Before: m.CandidateGeneration}}
	result, err = complete(m, gatewayRequest(m, files, domain.Worker, m.Plan.Steps[0]))
	if err != nil {
		t.Fatal(err)
	}
	var edits struct {
		Edits []Edit `json:"edits"`
	}
	_ = json.Unmarshal([]byte(result.Text), &edits)
	if len(edits.Edits) != 1 || edits.Edits[0].Path != "internal/label/label.go" || *edits.Edits[0].Content != "broken" {
		t.Fatal(result)
	}
	for _, mutate := range []func(*domain.Mission){func(m *domain.Mission) { m.Approvals = nil }, func(m *domain.Mission) { m.Approvals[1].PlanRevision = 2 }, func(m *domain.Mission) { m.CodingAttempts[0].StepID = "other" }} {
		raw, _ := json.Marshal(m)
		var bad domain.Mission
		_ = json.Unmarshal(raw, &bad)
		mutate(&bad)
		if _, err := complete(bad, gatewayRequest(bad, files, domain.Worker, bad.Plan.Steps[0])); err == nil {
			t.Fatal("worker without current authority")
		}
	}
}

func TestGatewayIdentityContextAndModeFailClosed(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Mission, map[string]string){
		"real":       func(m *domain.Mission, _ map[string]string) { m.Spec.C4.Mode = "real" },
		"controlled": func(m *domain.Mission, _ map[string]string) { m.Spec.C4.ControlVersion = 1 },
		"account": func(m *domain.Mission, _ map[string]string) {
			p := m.Profiles[domain.Worker]
			p.AccountID = "account"
			m.Profiles[domain.Worker] = p
		},
		"context":   func(_ *domain.Mission, f map[string]string) { f[Descriptor] = "{}" },
		"scope":     func(m *domain.Mission, _ map[string]string) { m.Spec.Scope = []string{"elsewhere"} },
		"goal":      func(m *domain.Mission, _ map[string]string) { m.Goal = "unrecognized" },
		"authority": func(m *domain.Mission, _ map[string]string) { m.Approvals = nil },
	} {
		t.Run(name, func(t *testing.T) {
			m, f := gatewayMission(t)
			mutate(&m, f)
			if _, err := complete(m, gatewayRequest(m, f, domain.Planner, nil)); err == nil {
				t.Fatal("accepted invalid authority/context")
			}
		})
	}
	m, f := gatewayMission(t)
	req := gatewayRequest(m, f, domain.Planner, nil)
	for _, mutate := range []func(*domain.Mission){func(m *domain.Mission) { m.ID = "other" }, func(m *domain.Mission) { m.Epoch++ }, func(m *domain.Mission) { m.CandidateGeneration.ID = "other" }} {
		copy := m
		g := *m.CandidateGeneration
		copy.CandidateGeneration = &g
		mutate(&copy)
		if _, err := complete(copy, req); err == nil {
			t.Fatal("accepted stale identity")
		}
	}
	if _, err := complete(m, gatewayRequest(m, f, domain.Researcher, nil)); err == nil {
		t.Fatal("fallback")
	}
}

func TestGatewayDebuggerRequiresCurrentRecordedFailure(t *testing.T) {
	m, f := gatewayMission(t)
	d, _ := Parse([]byte(example))
	plan := d.Plan()
	m.Plan = &plan
	m.State = "verifying"
	m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: "execute", PlanRevision: 1})
	m.Evidence = []domain.Verification{{ID: "v1", CandidateDigest: "digest", AcceptanceRevision: 1, SourceVersion: 1, SourceKind: "synthetic_stub"}}
	m.C4Operations = []domain.C4Operation{{ID: "v1", MissionID: m.ID, Epoch: 1, Kind: "candidate", PlanRevision: 1, CandidateGeneration: m.CandidateGeneration, CandidateDigest: "digest", State: "finished", Result: &domain.C4Result{Class: "test_failed"}, Cleanup: []domain.C4Cleanup{{State: "confirmed"}}}}
	m.CodingAttempts = []domain.CodingAttempt{{Phase: "verification", Status: "failed", FailureCode: "verification_failed", Epoch: 1, PlanRevision: 1, Before: m.CandidateGeneration, VerificationID: "v1"}, {Role: domain.Debugger, Phase: "debugger", Status: "running", Epoch: 1, PlanRevision: 1, Before: m.CandidateGeneration, RepairRound: 1}}
	result, err := complete(m, gatewayRequest(m, f, domain.Debugger, map[string]int{"repair_round": 1}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Edits []Edit }
	_ = json.Unmarshal([]byte(result.Text), &out)
	if len(out.Edits) != 1 || *out.Edits[0].Content != "normalized" {
		t.Fatal(result)
	}
	for _, mutate := range []func(*domain.Mission){func(m *domain.Mission) { m.Evidence[0].Passed = true }, func(m *domain.Mission) { m.Evidence[0].CandidateDigest = "stale" }, func(m *domain.Mission) { m.C4Operations[0].ID = "foreign" }, func(m *domain.Mission) { m.C4Operations[0].Result.Class = "outcome_unknown" }, func(m *domain.Mission) { m.C4Operations[0].Cleanup = nil }, func(m *domain.Mission) { m.CodingAttempts[0].VerificationID = "foreign" }} {
		raw, _ := json.Marshal(m)
		var bad domain.Mission
		_ = json.Unmarshal(raw, &bad)
		mutate(&bad)
		if _, err := complete(bad, gatewayRequest(bad, f, domain.Debugger, map[string]int{"repair_round": 1})); err == nil {
			t.Fatal("accepted unbound failure")
		}
	}
}

func TestGatewayIndependentReviewBindings(t *testing.T) {
	for _, role := range []domain.Role{domain.QA, domain.Auditor} {
		t.Run(string(role), func(t *testing.T) {
			m, f := gatewayMission(t)
			d, _ := Parse([]byte(example))
			p := d.Plan()
			m.Plan = &p
			m.State = "verifying"
			m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: "current", PlanRevision: 1})
			m.Evidence = []domain.Verification{{ID: "current-check", CandidateDigest: m.CandidateDigest, AcceptanceRevision: 1, Passed: true, SourceVersion: 1, SourceKind: "synthetic_stub", Results: []domain.CommandResult{{Command: m.Spec.Acceptance[0], ExitCode: 0}}}}
			m.CodingAttempts = []domain.CodingAttempt{{Phase: "verification", Status: "succeeded", Epoch: 1, PlanRevision: 1, Before: m.CandidateGeneration, After: m.CandidateGeneration, VerificationID: "current-check"}, {Phase: string(role), Role: role, Status: "running", Epoch: 1, PlanRevision: 1, Before: m.CandidateGeneration}}
			for i := range m.CodingAttempts {
				m.CodingAttempts[i].ID = fmt.Sprintf("attempt-%d", i)
				m.CodingAttempts[i].StartedAt = "2026-01-01T00:00:00Z"
			}
			m.CodingAttempts[0].FinishedAt = "2026-01-01T00:00:01Z"
			contract, err := domain.NewCodingReviewContract(m, role)
			if err != nil {
				t.Fatal(err)
			}
			result, err := complete(m, gatewayRequest(m, f, role, contract))
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Passed  bool                       `json:"passed"`
				Binding string                     `json:"binding_digest"`
				Checks  []domain.CodingReviewCheck `json:"checks"`
			}
			_ = json.Unmarshal([]byte(result.Text), &out)
			if !out.Passed || out.Binding != contract.BindingDigest || len(out.Checks) != len(contract.Required) {
				t.Fatal("review not bound", result)
			}
			contract.BindingDigest = "stale"
			if _, err := complete(m, gatewayRequest(m, f, role, contract)); err == nil {
				t.Fatal("accepted stale review contract")
			}
		})
	}
}
