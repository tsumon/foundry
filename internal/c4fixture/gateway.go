package c4fixture

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"reflect"
)

const Goal = "SYNTHETIC C4: refactor Double without changing its protected behavior"
const BaselineSource = "package demo\n\nfunc Double(n int) int { return n + n }\n"
const BrokenSource = "package demo\n\nfunc Double(n int) int { return n + 2 }\n"
const RepairedSource = "package demo\n\nfunc Double(n int) int { return n * 2 }\n"
const FrozenTest = "package demo\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n for _, n := range []int{-3, 0, 2, 7} {\n  if got := Double(n); got != n+n { t.Fatalf(\"Double(%d) = %d\", n, got) }\n }\n}\n"

// Dedicated, accountless gateway. It has no router, provider, fallback or
// credential interface. These are simulated judgements, not compiler results.
type Gateway struct{ store *store.Store }

func New(s *store.Store) *Gateway { return &Gateway{store: s} }

func (g Gateway) Complete(_ context.Context, req model.Request) (model.Result, error) {
	var input struct {
		Goal     string `json:"goal"`
		Identity struct {
			MissionID string               `json:"mission_id"`
			Epoch     int64                `json:"epoch"`
			Candidate domain.GenerationRef `json:"candidate_generation"`
		} `json:"coding_identity"`
		Extra domain.CodingReviewContract `json:"extra"`
	}
	if err := json.Unmarshal([]byte(req.Prompt), &input); err != nil {
		return model.Result{}, err
	}
	m, err := g.store.GetMission(input.Identity.MissionID)
	if err != nil || m.Goal != Goal || m.Spec.C4 == nil || m.Goal != input.Goal || m.Epoch != input.Identity.Epoch || m.CandidateGeneration == nil || *m.CandidateGeneration != input.Identity.Candidate {
		return model.Result{}, errors.New("C4 fixture requires exact local task/candidate identity")
	}
	var output any
	switch req.Role {
	case domain.Planner:
		output = map[string]any{"summary": "SYNTHETIC refactor proposal; separate human execution approval required", "steps": []any{map[string]any{"id": "double", "title": "Refactor Double", "description": "Equivalent arithmetic in main.go only; frozen tests and module unchanged", "files": []string{"main.go"}, "depends_on": []string{}}}}
	case domain.Worker, domain.Debugger:
		content := BrokenSource
		if req.Role == domain.Debugger {
			if len(m.Evidence) == 0 || m.Evidence[len(m.Evidence)-1].Passed || len(m.C4Operations) == 0 || m.C4Operations[len(m.C4Operations)-1].Result == nil || m.C4Operations[len(m.C4Operations)-1].Result.Class != "test_failed" {
				return model.Result{}, errors.New("C4 debugger requires recorded synthetic test failure")
			}
			content = RepairedSource
		}
		output = map[string]any{"summary": "SYNTHETIC local candidate edit", "edits": []any{map[string]any{"path": "main.go", "content": content}}}
	case domain.QA, domain.Auditor:
		contract, err := domain.NewCodingReviewContract(m, req.Role)
		if err != nil || !reflect.DeepEqual(input.Extra, contract) {
			return model.Result{}, errors.New("C4 review requires exact C2 controller contract")
		}
		checks := []domain.CodingReviewCheck{}
		passed := true
		for _, ref := range contract.Required {
			checks = append(checks, domain.CodingReviewCheck{ID: ref.ID, Passed: &passed, Summary: "SYNTHETIC judgement of supplied reference; no command executed", VerificationID: ref.VerificationID, AcceptanceIndex: ref.AcceptanceIndex, ReferenceDigest: ref.ReferenceDigest, Paths: []string{}})
		}
		output = map[string]any{"passed": true, "summary": "SYNTHETIC review, not real execution or readiness", "findings": []any{}, "binding_digest": contract.BindingDigest, "checks": checks}
	case domain.Main:
		output = map[string]string{"answer": "SYNTHETIC C4 fixture: baseline, context, execution and delivery remain separate decisions. These role judgments are local simulations and never establish real Docker execution."}
	default:
		return model.Result{}, errors.New("role not used by C4 fixture; no fallback")
	}
	raw, err := json.Marshal(output)
	return model.Result{Text: string(raw), UsageKnown: true}, err
}
