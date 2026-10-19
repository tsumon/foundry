package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/store"
	"foundry/internal/workspace"
)

func revisionRequest(m domain.Mission) PlanRevisionRequest {
	var p EditablePlan
	raw, _ := json.Marshal(m.Plan)
	_ = json.Unmarshal(raw, &p)
	p.Summary = "Revised implementation"
	return PlanRevisionRequest{ExpectedMissionRevision: m.Revision, ExpectedPlanRevision: m.Plan.Revision, Reason: "Clarify implementation", Plan: p}
}

func readyRevision(t *testing.T, generations bool, extra ...string) (*Engine, *store.Store, *scripted, *fakeRunner, domain.Mission) {
	t.Helper()
	outputs := append([]string{research, plan}, extra...)
	setupFn := setup
	if generations {
		setupFn = generationSetup
	}
	e, s, g, r, m := setupFn(t, outputs...)
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	// Waiting for state alone may precede release of the serial controller.
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op != nil {
		<-op.done
	}
	return e, s, g, r, m
}

type revisionNoRunner struct{ t *testing.T }

func (r revisionNoRunner) Resolve(context.Context, string) (string, error) {
	r.t.Error("revision resolved image")
	return "", errors.New("forbidden")
}
func (r revisionNoRunner) Run(context.Context, string, string, string, string) (domain.CommandResult, error) {
	r.t.Error("revision ran verifier")
	return domain.CommandResult{}, errors.New("forbidden")
}
func (r revisionNoRunner) Reconcile(context.Context) error { return nil }

func TestRevisePlanPreservesAuthorityAndIndependentRevisions(t *testing.T) {
	for _, gen := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "generation"}[gen], func(t *testing.T) {
			e, s, g, _, m := readyRevision(t, gen, `{"answer":"comment only"}`)
			original := m
			var err error
			m, err = e.Chat(context.Background(), m.ID, m.Revision, "Explain plan")
			if err != nil {
				t.Fatal(err)
			}
			if m.Revision == m.Plan.Revision {
				t.Fatal("chat failed to diverge revisions")
			}
			e.runner = revisionNoRunner{t}
			before := m
			request := revisionRequest(m)
			request.Plan.Steps[0].Files = append(request.Plan.Steps[0].Files, "src/new.txt")
			next, record, err := e.RevisePlan(context.Background(), m.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			if next.Revision != before.Revision+1 || next.Plan.Revision != before.Plan.Revision+1 || record.PlanRevision != next.Plan.Revision || record.MissionRevision != next.Revision || record.ParentPlanRevision != before.Plan.Revision || record.Source != "user" || record.Plan.Steps[0].Status != "" {
				t.Fatalf("wrong revision semantics: %+v %+v", next, record)
			}
			if next.State != "awaiting_approval" || next.Epoch != before.Epoch || !reflect.DeepEqual(next.Budget, before.Budget) || !reflect.DeepEqual(next.Spec, before.Spec) || !reflect.DeepEqual(next.Profiles, before.Profiles) || !reflect.DeepEqual(next.Evidence, before.Evidence) || !reflect.DeepEqual(next.CandidateGeneration, before.CandidateGeneration) || next.CandidateDigest != before.CandidateDigest || len(next.Approvals) != len(before.Approvals)+1 || next.Plan.Steps[0].Status != "pending" {
				t.Fatal("revision changed frozen state")
			}
			seal := next.Approvals[len(next.Approvals)-1]
			if seal.Kind != "plan_proposal" || seal.PlanRevision != next.Plan.Revision || seal.Digest == before.Approvals[len(before.Approvals)-1].Digest {
				t.Fatal("new proposal missing")
			}
			if _, err = e.Approve(context.Background(), next.ID, before.Revision); !errors.Is(err, store.ErrConflict) {
				t.Fatalf("old approval accepted: %v", err)
			}
			request = revisionRequest(next)
			request.Plan.Summary = "Second revision"
			next, _, err = e.RevisePlan(context.Background(), next.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			history, err := s.ListPlanRevisions(next.ID)
			if err != nil || len(history) != 3 || history[0].Source != "planner" || history[0].PlanRevision != original.Plan.Revision || history[0].MissionRevision != before.Revision || history[2].ParentPlanRevision != history[1].PlanRevision {
				t.Fatalf("history: %+v %v", history, err)
			}
			if len(g.roles) != 3 {
				t.Fatal("revision dispatched model")
			}
		})
	}
}

func TestRevisePlanRejectsInvalidInputWithoutMutation(t *testing.T) {
	e, s, _, _, m := readyRevision(t, false)
	tests := map[string]func(*PlanRevisionRequest){
		"empty reason":  func(r *PlanRevisionRequest) { r.Reason = " \n" },
		"reason limit":  func(r *PlanRevisionRequest) { r.Reason = strings.Repeat("x", 1001) },
		"reason utf8":   func(r *PlanRevisionRequest) { r.Reason = string([]byte{255}) },
		"summary blank": func(r *PlanRevisionRequest) { r.Plan.Summary = " \n" },
		"summary limit": func(r *PlanRevisionRequest) { r.Plan.Summary = strings.Repeat("x", 4001) },
		"summary utf8":  func(r *PlanRevisionRequest) { r.Plan.Summary = string([]byte{255}) },
		"zero steps":    func(r *PlanRevisionRequest) { r.Plan.Steps = nil },
		"seven steps": func(r *PlanRevisionRequest) {
			for len(r.Plan.Steps) < 7 {
				r.Plan.Steps = append(r.Plan.Steps, r.Plan.Steps[0])
			}
		},
		"id blank":          func(r *PlanRevisionRequest) { r.Plan.Steps[0].ID = " \n" },
		"id limit":          func(r *PlanRevisionRequest) { r.Plan.Steps[0].ID = strings.Repeat("x", 65) },
		"id utf8":           func(r *PlanRevisionRequest) { r.Plan.Steps[0].ID = string([]byte{255}) },
		"duplicate id":      func(r *PlanRevisionRequest) { r.Plan.Steps = append(r.Plan.Steps, r.Plan.Steps[0]) },
		"title blank":       func(r *PlanRevisionRequest) { r.Plan.Steps[0].Title = " \n" },
		"title limit":       func(r *PlanRevisionRequest) { r.Plan.Steps[0].Title = strings.Repeat("x", 201) },
		"title utf8":        func(r *PlanRevisionRequest) { r.Plan.Steps[0].Title = string([]byte{255}) },
		"description blank": func(r *PlanRevisionRequest) { r.Plan.Steps[0].Description = " \n" },
		"description limit": func(r *PlanRevisionRequest) { r.Plan.Steps[0].Description = strings.Repeat("x", 8001) },
		"description utf8":  func(r *PlanRevisionRequest) { r.Plan.Steps[0].Description = string([]byte{255}) },
		"unknown dep":       func(r *PlanRevisionRequest) { r.Plan.Steps[0].DependsOn = []string{"unknown"} },
		"cycle":             func(r *PlanRevisionRequest) { r.Plan.Steps[0].DependsOn = []string{"one"} },
		"duplicate dep": func(r *PlanRevisionRequest) {
			second := r.Plan.Steps[0]
			second.ID = "two"
			second.DependsOn = []string{"one", "one"}
			r.Plan.Steps = append(r.Plan.Steps, second)
		},
		"empty files":    func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = nil },
		"duplicate file": func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = []string{"src/main.txt", "src/main.txt"} },
		"file limit": func(r *PlanRevisionRequest) {
			for i := 0; i < 30; i++ {
				r.Plan.Steps[0].Files = append(r.Plan.Steps[0].Files, "src/a"+strings.Repeat("a", i))
			}
		},
		"outside scope":           func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = []string{"other/new.txt"} },
		"protected new":           func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = []string{"src/new.test.ts"} },
		"sensitive new":           func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = []string{"src/secret.txt"} },
		"traversal":               func(r *PlanRevisionRequest) { r.Plan.Steps[0].Files = []string{"src/../outside"} },
		"noop":                    func(r *PlanRevisionRequest) { r.Plan.Summary = m.Plan.Summary },
		"noop empty dependencies": func(r *PlanRevisionRequest) { r.Plan.Summary = m.Plan.Summary; r.Plan.Steps[0].DependsOn = nil },
	}
	before, _ := json.Marshal(m)
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := revisionRequest(m)
			mutate(&request)
			if _, _, err := e.RevisePlan(context.Background(), m.ID, request); !errors.Is(err, ErrInvalidPlanRevision) {
				t.Fatalf("want invalid revision, got %v", err)
			}
			got, _ := s.GetMission(m.ID)
			after, _ := json.Marshal(got)
			h, _ := s.ListPlanRevisions(m.ID)
			if string(before) != string(after) || len(h) != 0 {
				t.Fatal("invalid revision mutated mission/history")
			}
		})
	}
}

func TestRevisePlanRejectsStateDriftAndDualCAS(t *testing.T) {
	for _, name := range []string{"mission CAS", "plan CAS", "active", "draft", "running", "verifying", "paused", "blocked", "delivered", "awaiting_delivery", "base drift", "candidate drift", "proposal drift", "pending", "prepared"} {
		t.Run(name, func(t *testing.T) {
			e, s, _, _, m := readyRevision(t, true)
			request := revisionRequest(m)
			want := ErrInvalidPlanRevision
			switch name {
			case "mission CAS":
				request.ExpectedMissionRevision--
				want = store.ErrConflict
			case "plan CAS":
				request.ExpectedPlanRevision--
				want = store.ErrConflict
			case "active":
				e.mu.Lock()
				e.active = &operation{}
				e.mu.Unlock()
				defer func() { e.mu.Lock(); e.active = nil; e.mu.Unlock() }()
			case "base drift":
				if err := os.WriteFile(filepath.Join(e.dir(m.ID), "base/src/main.txt"), []byte("drift"), 0600); err != nil {
					t.Fatal(err)
				}
			case "candidate drift":
				p, err := e.selectedCandidate(m)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(p, "src/main.txt"), []byte("drift"), 0600); err != nil {
					t.Fatal(err)
				}
			case "proposal drift":
				m.Plan.Summary = "unsealed"
				var err error
				m, err = s.UpdateMission(m.ID, m.Revision, "test.drift", func(n *domain.Mission) error { n.Plan = m.Plan; return nil })
				if err != nil {
					t.Fatal(err)
				}
				request.ExpectedMissionRevision = m.Revision
			case "pending", "prepared":
				op, err := e.localIntent(m, "reset", nil)
				if err != nil {
					t.Fatal(err)
				}
				op, err = s.BeginLocalOperation(op)
				if err != nil {
					t.Fatal(err)
				}
				if name == "prepared" {
					ref, _, prepareErr := workspace.PrepareGeneration(e.dir(m.ID), op.ID, filepath.Join(e.dir(m.ID), "base"), m.CandidateGeneration, nil, m.Spec.Scope, m.Spec.Protected)
					if prepareErr != nil {
						t.Fatal(prepareErr)
					}
					_, err = s.PrepareLocalOperation(op.ID, domain.LocalOperationResult{Generation: &ref})
					if err != nil {
						t.Fatal(err)
					}
				}
			default:
				var err error
				m, err = s.UpdateMission(m.ID, m.Revision, "test.state", func(n *domain.Mission) error { n.State = name; return nil })
				if err != nil {
					t.Fatal(err)
				}
				request.ExpectedMissionRevision = m.Revision
			}
			// Stale CAS must win even if expensive filesystem preflight would fail.
			if name == "mission CAS" || name == "plan CAS" {
				if err := os.Remove(filepath.Join(e.dir(m.ID), "base/src/main.txt")); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.GetMission(m.ID)
			if _, _, err := e.RevisePlan(context.Background(), m.ID, request); !errors.Is(err, want) {
				t.Fatalf("want %v got %v", want, err)
			}
			after, _ := s.GetMission(m.ID)
			h, _ := s.ListPlanRevisions(m.ID)
			if !reflect.DeepEqual(before, after) || len(h) != 0 {
				t.Fatal("rejected revision mutated state")
			}
		})
	}
}

func TestRevisePlanHistoryInsertFailureRollsBack(t *testing.T) {
	e, s, _, _, m := readyRevision(t, false)
	db := failureDB(t, e)
	if _, err := db.Exec(`CREATE TRIGGER fail_user_revision BEFORE INSERT ON plan_revisions WHEN json_extract(NEW.data,'$.source')='user' BEGIN SELECT RAISE(ABORT,'test history failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.RevisePlan(context.Background(), m.ID, revisionRequest(m)); err == nil {
		t.Fatal("insert failure ignored")
	}
	got, _ := s.GetMission(m.ID)
	h, _ := s.ListPlanRevisions(m.ID)
	if !reflect.DeepEqual(got, m) || len(h) != 0 {
		t.Fatal("partial revision committed")
	}
}

func TestPlannerUsesRevisionExecutableValidation(t *testing.T) {
	for _, invalid := range []string{
		strings.Replace(plan, `"description":"update"`, `"description":" "`, 1),
		strings.Replace(plan, `"files":["src/main.txt"]`, `"files":["src/main.txt","src/main.txt"]`, 1),
		strings.Replace(plan, `"summary":"change"`, `"summary":"`+strings.Repeat("x", 4001)+`"`, 1),
	} {
		e, s, _, _, m := setup(t, research, invalid)
		if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
			t.Fatal(err)
		}
		wait(t, s, m.ID, "blocked")
	}
}

func TestPlannerRejectsRawInvalidUTF8(t *testing.T) {
	e, s, _, _, m := setup(t, research, strings.Replace(plan, "update", string([]byte{255}), 1))
	if _, err := e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	wait(t, s, m.ID, "blocked")
}

func TestRevisePlanRetainsHistoryAcrossReplanEpochs(t *testing.T) {
	e, s, _, _, m := readyRevision(t, true, `{"unexpected":true}`, research, plan)
	first, _, err := e.RevisePlan(context.Background(), m.ID, revisionRequest(m))
	if err != nil {
		t.Fatal(err)
	}
	// An explicitly approved run blocks on invalid worker output, then the
	// user explicitly replans into a new epoch.
	if _, err = e.Approve(context.Background(), m.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	blocked := wait(t, s, m.ID, "blocked")
	e.mu.Lock()
	failedOp := e.active
	e.mu.Unlock()
	if failedOp != nil {
		<-failedOp.done
	}
	if _, err = e.Plan(context.Background(), m.ID, blocked.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op != nil {
		<-op.done
	}
	next, record, err := e.RevisePlan(context.Background(), m.ID, revisionRequest(m))
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.ListPlanRevisions(m.ID)
	if err != nil || len(h) != 4 || h[0].Epoch != first.Epoch || h[1].Epoch != first.Epoch || h[2].Epoch != m.Epoch || h[2].Source != "planner" || h[2].ParentPlanRevision != 0 || h[3].Epoch != m.Epoch || record.ParentPlanRevision != m.Plan.Revision || next.Plan.Revision != m.Plan.Revision+1 || m.Epoch <= first.Epoch {
		t.Fatalf("incorrect epoch history: %+v %v", h, err)
	}
}

func TestRevisedPlanNeedsFreshExecuteSeal(t *testing.T) {
	e, s, _, _, m := readyRevision(t, false, edit, review, review)
	oldSeal := m.Approvals[len(m.Approvals)-1]
	var err error
	m, err = s.UpdateMission(m.ID, m.Revision, "test.old_authority", func(n *domain.Mission) error {
		n.Approvals = append(n.Approvals, domain.Approval{Kind: "execute", Digest: oldSeal.Digest, Revision: n.Revision})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _, err = e.RevisePlan(context.Background(), m.ID, revisionRequest(m))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.candidate(m); err == nil {
		t.Fatal("old execute seal authorized revised plan")
	}
	// A saved revision alone cannot run; the existing explicit approval can.
	approved, err := e.Approve(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	seal := approved.Approvals[len(approved.Approvals)-1]
	if seal.Kind != "execute" || seal.PlanRevision != m.Plan.Revision || seal.Digest == oldSeal.Digest {
		t.Fatal("execute seal not bound to revised plan")
	}
	delivered := wait(t, s, m.ID, "awaiting_delivery")
	if delivered.Evidence[0].AcceptanceRevision != m.Plan.Revision {
		t.Fatal("verification bound to wrong plan")
	}
}

func TestRevisePlanAcceptsExactTextAndCountLimits(t *testing.T) {
	e, _, _, _, m := readyRevision(t, false)
	request := revisionRequest(m)
	request.Reason = strings.Repeat("界", 333) + "x"
	request.Plan.Summary = strings.Repeat("x", 4000)
	request.Plan.Steps = nil
	for i := 0; i < 6; i++ {
		step := EditableStep{ID: strings.Repeat("a", 63) + string(rune('a'+i)), Title: strings.Repeat("x", 200), Description: strings.Repeat("x", 8000), DependsOn: []string{}, Files: []string{}}
		if i > 0 {
			step.DependsOn = []string{request.Plan.Steps[i-1].ID}
		}
		for j := 0; j < 30; j++ {
			step.Files = append(step.Files, "src/"+strings.Repeat("f", j+1)+".txt")
		}
		request.Plan.Steps = append(request.Plan.Steps, step)
	}
	if _, _, err := e.RevisePlan(context.Background(), m.ID, request); err != nil {
		t.Fatal(err)
	}
}
