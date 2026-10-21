package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const codingPass = "coding-review-pass"
const codingReject = "coding-review-reject"

type codingReviewGateway struct {
	script    *scripted
	mutate    func(domain.Role, map[string]any)
	contracts map[domain.Role]domain.CodingReviewContract
}

func (g *codingReviewGateway) Complete(ctx context.Context, req model.Request) (model.Result, error) {
	r, err := g.script.Complete(ctx, req)
	if err != nil || (r.Text != codingPass && r.Text != codingReject) {
		return r, err
	}
	var prompt struct {
		Extra domain.CodingReviewContract `json:"extra"`
	}
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		return r, err
	}
	c := prompt.Extra
	if c.BindingDigest == "" || c.MissionID == "" {
		return r, errors.New("fixture requires supplied review contract")
	}
	if g.contracts != nil {
		g.contracts[req.Role] = c
	}
	checks := []map[string]any{}
	for _, ref := range c.Required {
		check := map[string]any{"id": ref.ID, "passed": true, "summary": "fixture assessment of supplied references, not test execution", "paths": []string{}}
		if req.Role == domain.QA {
			check["verification_id"], check["acceptance_index"] = ref.VerificationID, *ref.AcceptanceIndex
		} else {
			check["reference_digest"] = ref.ReferenceDigest
		}
		checks = append(checks, check)
	}
	output := map[string]any{"passed": r.Text == codingPass, "summary": "fixture review", "findings": []any{}, "binding_digest": c.BindingDigest, "checks": checks}
	if g.mutate != nil {
		g.mutate(req.Role, output)
	}
	raw, err := json.Marshal(output)
	r.Text = string(raw)
	return r, err
}

func codingReady(t *testing.T, mutate func(domain.Role, map[string]any), qa, auditor string) (*Engine, domain.Mission, *codingReviewGateway) {
	t.Helper()
	e, s, g, _, m := codingSetup(t, plan, edit, qa, auditor)
	gateway := &codingReviewGateway{script: g, mutate: mutate, contracts: map[domain.Role]domain.CodingReviewContract{}}
	e.gateway = gateway
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
	return e, m, gateway
}

func TestCodingReviewContractsGateActualDelivery(t *testing.T) {
	for _, name := range []string{"valid", "missing", "duplicate", "foreign", "stale", "omitted_boolean", "wrong_candidate_plan", "failed_check", "fake_path", "blocking_finding", "auditor_qa_reference", "auditor_empty_qa_field", "qa_empty_auditor_field", "top_alias", "check_alias"} {
		t.Run(name, func(t *testing.T) {
			e, m, g := codingReady(t, func(role domain.Role, out map[string]any) {
				checks := out["checks"].([]map[string]any)
				if role == domain.Auditor && name == "auditor_qa_reference" {
					checks[0]["verification_id"] = "foreign"
					return
				}
				if role == domain.Auditor && name == "auditor_empty_qa_field" {
					checks[0]["verification_id"] = ""
					return
				}
				if role != domain.QA {
					return
				}
				switch name {
				case "top_alias":
					out["Passed"] = out["passed"]
					delete(out, "passed")
				case "check_alias":
					checks[0]["Passed"] = checks[0]["passed"]
					delete(checks[0], "passed")
				case "qa_empty_auditor_field":
					checks[0]["reference_digest"] = ""
				case "missing":
					out["checks"] = []any{}
				case "duplicate":
					out["checks"] = append(checks, checks[0])
				case "foreign":
					checks[0]["id"] = "acceptance:9"
				case "stale":
					checks[0]["verification_id"] = "old-verification"
				case "omitted_boolean":
					delete(checks[0], "passed")
				case "wrong_candidate_plan":
					out["binding_digest"] = strings.Repeat("a", 64)
				case "failed_check":
					checks[0]["passed"] = false
				case "fake_path":
					checks[0]["paths"] = []string{"invented.txt"}
				case "blocking_finding":
					out["findings"] = []any{map[string]any{"severity": "blocking", "summary": "not ready", "paths": []string{}}}
				}
			}, codingPass, codingPass)
			if name == "valid" {
				if m.State != "awaiting_delivery" {
					t.Fatalf("valid contract blocked: %s", m.Error)
				}
				qa, au := g.contracts[domain.QA], g.contracts[domain.Auditor]
				if len(qa.Required) != 1 || qa.Required[0].ID != "acceptance:0" || len(au.Required) != 4 || au.Required[0].ID != "scope" || qa.TraceDigest != au.TraceDigest {
					t.Fatal("role references not distinct/stable", qa, au)
				}
				if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				if m.State != "blocked" {
					t.Fatalf("invalid contract acquired authority: %s", m.State)
				}
				if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
					t.Fatal("invalid contract delivered")
				}
				last := m.CodingAttempts[len(m.CodingAttempts)-1]
				if last.FailureCode != "invalid_model_output" {
					t.Fatal("refusal not persisted", last)
				}
			}
		})
	}
}

func TestCodingReviewsInvalidateOnRelevantMaterialChanges(t *testing.T) {
	_, ready, _ := codingReady(t, nil, codingPass, codingPass)
	for _, name := range []string{"plan", "epoch", "candidate", "trace", "evidence", "failed_command", "approval", "scope"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(ready)
			var m domain.Mission
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "plan":
				m.Plan.Summary = "changed"
			case "epoch":
				m.Epoch++
			case "candidate":
				m.CandidateGeneration.ID = "different"
			case "trace":
				m.CodingAttempts[0].InvocationID = "changed"
			case "evidence":
				m.Evidence[0].Results[0].Output = "changed"
			case "failed_command":
				m.Evidence[0].Results[0].ExitCode = 1
			case "approval":
				m.Approvals = nil
			case "scope":
				m.Spec.Scope = []string{"other/"}
			}
			if err := domain.CanDeliver(m, m.CandidateDigest); err == nil {
				t.Fatal("stale review remained valid")
			}
		})
	}
}

func TestCodingReportIncludesTraceAndDeliveredReadsRejectTampering(t *testing.T) {
	for _, name := range []string{"trace", "plan", "approval", "delivery_approval", "review"} {
		t.Run(name, func(t *testing.T) {
			e, m, _ := codingReady(t, nil, codingPass, codingPass)
			m, err := e.Deliver(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			report, err := e.ReadDelivery(m.ID, "report")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(report), `"coding_attempts"`) || !strings.Contains(string(report), `"plan"`) {
				t.Fatal("C2 report omitted trace/plan")
			}
			switch name {
			case "trace":
				m.CodingAttempts[0].InvocationID = "changed"
			case "plan":
				m.Plan.Summary = "changed"
			case "approval":
				m.Approvals = nil
			case "delivery_approval":
				m.Approvals = m.Approvals[:len(m.Approvals)-1]
			case "review":
				m.Reviews[0].Checks[0].Summary = "changed"
			}
			raw, _ := json.Marshal(m)
			db := failureDB(t, e)
			if _, err = db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"patch", "report"} {
				if _, err := e.ReadDelivery(m.ID, kind); err == nil {
					t.Fatal("tampered delivered material downloaded", kind)
				}
			}
			if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
				t.Fatal("delivered shortcut accepted changed material")
			}
		})
	}
}

func TestCodingReviewerDisagreementEntersFailureBundle(t *testing.T) {
	e, m, _ := codingReady(t, nil, codingPass, codingReject)
	if m.State != "blocked" || len(m.Reviews) != 2 || !m.Reviews[0].Passed || m.Reviews[1].Passed {
		t.Fatalf("review disagreement lost: %+v", m)
	}
	_, bundle, err := e.freezeReplan(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Failures) != 1 || bundle.Failures[0].Code != "review_rejected" || bundle.Failures[0].ReviewRole != domain.Auditor {
		t.Fatal("rejection not available to next explicit replan", bundle)
	}
}

func TestCodingAuditorControllerRejectsInventedChangeEvidence(t *testing.T) {
	e, m, _ := codingReady(t, nil, codingPass, codingPass)
	m.Changes[0].After = "invented content not on candidate"
	if err := e.checkCodingReviewMaterial(m); err == nil {
		t.Fatal("invented change evidence accepted as Auditor material")
	}
}

func TestCodingAuditorControllerRejectsOutOfPlanAndChangedBase(t *testing.T) {
	for _, name := range []string{"out_of_plan", "base"} {
		t.Run(name, func(t *testing.T) {
			e, m, _ := codingReady(t, nil, codingPass, codingPass)
			if name == "base" {
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "base", "src/main.txt"), []byte("tampered"), 0644); err != nil {
					t.Fatal(err)
				}
			} else {
				candidate, err := e.selectedCandidate(m)
				if err != nil {
					t.Fatal(err)
				}
				path := "src/unplanned.txt"
				// Construct a disposable, sealed generation under a deliberately
				// broader fixture scope; it must not pass current plan authority.
				id, _ := opaque()
				ref, _, err := workspace.PrepareGeneration(e.dir(m.ID), id, candidate, m.CandidateGeneration, []workspace.Edit{{Path: path, Content: "changed"}}, []string{"src/", "tests/"}, nil)
				if err != nil {
					t.Fatal(err)
				}
				m.CandidateGeneration, m.CandidateDigest = &ref, ref.Digest
			}
			if err := e.checkCodingReviewMaterial(m); err == nil {
				t.Fatal("unapproved tree accepted", name)
			}
		})
	}
}

func TestCodingProtectedProposalCannotExecuteOrReachReviews(t *testing.T) {
	protectedPlan := strings.ReplaceAll(plan, "src/main.txt", "src/acceptance.test.txt")
	e, s, g, r, m := codingSetup(t, protectedPlan, edit, codingPass, codingPass)
	initial := *m.CandidateGeneration
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || len(g.roles) != 1 || r.calls != 0 || len(m.Reviews) != 0 || m.CandidateDigest != initial.Digest {
		t.Fatal("protected proposal acquired execution or review authority")
	}
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("protected plan approved")
	}
}

func TestCodingPauseFencesLateReviewAndRecoveryDoesNotReplay(t *testing.T) {
	e, s, g, r, m := codingSetup(t, plan, edit, codingPass, codingPass)
	entered, release := make(chan struct{}), make(chan struct{})
	e.gateway = &codingReviewGateway{script: g, mutate: func(role domain.Role, _ map[string]any) {
		if role == domain.QA {
			close(entered)
			<-release
		}
	}}
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	<-entered
	m, _ = s.GetMission(m.ID)
	paused := make(chan error, 1)
	go func() { _, err := e.Pause(context.Background(), m.ID, m.Revision); paused <- err }()
	wait(t, s, m.ID, "pausing")
	close(release)
	if err := <-paused; err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetMission(m.ID)
	if len(got.Reviews) != 0 || got.CodingAttempts[len(got.CodingAttempts)-1].Status != "interrupted" || len(g.roles) != 3 || r.calls != 1 {
		t.Fatal("late review accepted or continued", got.State)
	}
	restart := New(s, e.gateway, Options{DataDir: e.options.DataDir, Runner: r})
	if err := restart.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(g.roles) != 3 || r.calls != 1 {
		t.Fatal("review replayed on recovery")
	}
}

func TestCodingProseReviewBlocksRealExecution(t *testing.T) {
	e, s, _, _, m := codingSetup(t, plan, edit, review, review)
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
	if m.State != "blocked" {
		t.Fatalf("passing prose acquired delivery authority: %s", m.State)
	}
	if _, err := e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("invalid review delivered")
	}
}
