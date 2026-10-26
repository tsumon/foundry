package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInferenceBudgetUsesOnlyAppliedEffectiveContinuation(t *testing.T) {
	s, m, catalogID := continuationModelFixture(t, domain.OpenAIInferenceModel)
	c, err := s.GetModelAcceptance(m.ModelSnapshot.Roles[domain.Planner].Account.ID, catalogID)
	if err != nil {
		t.Fatal(err)
	}
	a, r := c.Material.Account, c.Material.Route
	admission := domain.ProductionAdmission{Generation: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Profile: "openai_siwc_inference_v1"}
	now := time.Now()
	original := domain.AcceptanceDigest(m.ModelSnapshot)
	if _, err = s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now); err == nil {
		t.Fatal("old grant adopted new authorization")
	}
	m, err = s.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalogID, now)
	if err != nil {
		t.Fatal(err)
	}
	continuation := m.ModelContinuations[0]
	if _, err = s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now); err == nil {
		t.Fatal("unapplied continuation adopted")
	}
	m, err = s.ApproveModelContinuation(m.ID, continuation.ID, m.Revision, continuation.Digest, now)
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.ApplyModelContinuation(m.ID, continuation.ID, m.Revision, continuation.Digest, now)
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Material.InferenceBudget.SnapshotDigest != continuation.Replacement.Digest || domain.AcceptanceDigest(m.ModelSnapshot) != original {
		t.Fatal("wrong effective snapshot")
	}
	o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.Revision++
	m.UpdatedAt = got.UpdatedAt
	m.Budget.UsedCalls++
	m.Budget.UsageUnknown = true
	if !reflect.DeepEqual(got, m) {
		t.Fatal("diagnostic changed task history/approvals/continuation")
	}
}

func TestInferenceBudgetRejectsAbsentWrongTaskAndUnrelatedPhase(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	now := time.Now()
	for _, id := range []string{"", "missing-task"} {
		q := inferenceProposal(a, r, c)
		q.BudgetMissionID = id
		if _, e := s.ProposeProductionInference(a.ID, q, admission, now); e == nil {
			t.Fatal("missing task accepted")
		}
	}
	q := inferenceProposal(a, r, c)
	q.ExpectedMissionRevision = 0
	if _, e := s.ProposeProductionInference(a.ID, q, admission, now); e == nil {
		t.Fatal("zero revision accepted")
	}
	q.Phase = "catalog"
	admission.Profile = "openai_siwc_catalog_v1"
	if _, e := s.ProposeProductionCatalog(a.ID, q, admission, now); e == nil {
		t.Fatal("budget fields accepted on catalog")
	}
}

func TestInferenceBudgetRejectsMalformedTaskExecutionHistory(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	if _, err := s.db.Exec(`UPDATE missions SET data=json_set(data,'$.c4_operations','invalid synthetic history') WHERE id='inference-budget-task'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, time.Now()); err == nil {
		t.Fatal("malformed execution history accepted")
	}
}

func inferenceBudgetMission(t *testing.T, s *Store, a domain.ModelAccount, r domain.ModelRoute) domain.Mission {
	t.Helper()
	if m, err := s.GetMission("inference-budget-task"); err == nil {
		return m
	}
	p := domain.Profile{ID: "inference-budget-profile", Name: "Synthetic", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: domain.OpenAIInferenceModel}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := s.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMission(domain.Mission{ID: "inference-budget-task", State: "blocked", Epoch: 1, WorkspaceMode: "generations_account_snapshot_v2", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{UsedCalls: 3, MaxCalls: 7}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInferenceBudgetBindingGatesAtEveryBoundary(t *testing.T) {
	for _, stage := range []string{"prepare", "approve", "claim", "pre-send"} {
		for _, change := range []string{"revision", "epoch", "budget", "cap", "active", "account", "route", "authorization", "model", "snapshot", "inflight"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				s, a, r, c, admission := inferenceLedgerFixture(t)
				m, _ := s.GetMission("inference-budget-task")
				q := boundInferenceProposal(t, a, r, c, m)
				now := time.Now()
				var o domain.ModelAcceptanceOperation
				var e error
				if stage != "prepare" {
					o, e = s.ProposeProductionInference(a.ID, q, admission, now)
					if e != nil {
						t.Fatal(e)
					}
				}
				if stage == "claim" || stage == "pre-send" {
					o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
					if e != nil {
						t.Fatal(e)
					}
				}
				if stage == "pre-send" {
					o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
					if e != nil {
						t.Fatal(e)
					}
					if e = s.ReserveProductionInferenceDispatch(o, admission, now); e != nil {
						t.Fatal(e)
					}
					m, _ = s.GetMission(m.ID)
				}
				switch change {
				case "revision":
					m.Revision++
				case "epoch":
					m.Epoch++
				case "budget":
					m.Budget.UsedCalls++
				case "cap":
					m.Budget.UsedCalls = 7
				case "active":
					m.State = "planning"
				case "snapshot":
					m.ModelSnapshot = nil
				case "inflight":
					_, e = s.db.Exec(`INSERT INTO invocations(id,mission_id,epoch,session_id,ordinal,kind,role,name,binding_digest,authority_digest,grant_id,intent,status,reserved_result_bytes,created_at) VALUES('active-test',?,1,'session',1,'model','researcher','',?,?,'','{}','authorized_pending',0,'synthetic')`, m.ID, m.ModelSnapshot.Digest, m.ModelSnapshot.Digest)
					if e != nil {
						t.Fatal(e)
					}
				default:
					for role, selection := range m.ModelSnapshot.Roles {
						switch change {
						case "account":
							selection.Account.Name = "changed"
						case "route":
							selection.Route.ID = "changed"
						case "authorization":
							selection.Authorization.Subject = "changed"
						case "model":
							selection.Profile.Model = "other"
						}
						m.ModelSnapshot.Roles[role] = selection
					}
				}
				raw, _ := json.Marshal(m)
				if _, e = s.db.Exec(`UPDATE missions SET data=?,revision=?,state=? WHERE id=?`, raw, m.Revision, m.State, m.ID); e != nil {
					t.Fatal(e)
				}
				switch stage {
				case "prepare":
					_, e = s.ProposeProductionInference(a.ID, q, admission, now)
				case "approve":
					_, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
				case "claim":
					_, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
				case "pre-send":
					e = s.ValidateProductionInferenceDispatch(o, admission, now)
				}
				// A proposal freezes the current epoch/budget; only the caller's revision is stale.
				if stage == "prepare" && (change == "epoch" || change == "budget") {
					if e != nil {
						t.Fatal(e)
					}
					return
				}
				if e == nil {
					t.Fatal("changed task authority accepted")
				}
			})
		}
	}
}

func TestInferenceBudgetConcurrentProposalsAndClaimRollback(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	m, _ := s.GetMission("inference-budget-task")
	now := time.Now()
	var approved []domain.ModelAcceptanceOperation
	for range 2 {
		o, e := s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now)
		if e != nil {
			t.Fatal(e)
		}
		o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
		if e != nil {
			t.Fatal(e)
		}
		approved = append(approved, o)
	}
	if _, e := s.db.Exec(`CREATE TRIGGER reject_budget BEFORE INSERT ON model_acceptance_inference_budgets BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END;`); e != nil {
		t.Fatal(e)
	}
	o := approved[0]
	if _, e := s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now); e == nil {
		t.Fatal("rollback injection missed")
	}
	got, _ := s.GetMission(m.ID)
	if got.Revision != 1 || got.Budget.UsedCalls != 3 {
		t.Fatal("failed claim consumed budget")
	}
	var claims int
	if e := s.db.QueryRow(`SELECT count(*) FROM model_acceptance_claims WHERE operation_id=?`, o.ID).Scan(&claims); e != nil || claims != 0 {
		t.Fatal("failed claim persisted")
	}
	if _, e := s.db.Exec(`DROP TRIGGER reject_budget`); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	var wins atomic.Int32
	for _, candidate := range approved {
		for range 4 {
			wg.Add(1)
			go func(o domain.ModelAcceptanceOperation) {
				defer wg.Done()
				if _, e := s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now); e == nil {
					wins.Add(1)
				}
			}(candidate)
		}
	}
	wg.Wait()
	got, _ = s.GetMission(m.ID)
	if wins.Load() != 1 || got.Revision != 2 || got.Budget.UsedCalls != 4 || got.State != "blocked" {
		t.Fatalf("concurrent debit: %d %+v", wins.Load(), got.Budget)
	}
	if e := s.db.QueryRow(`SELECT count(*) FROM model_acceptance_inference_budgets WHERE mission_id=?`, m.ID).Scan(&claims); e != nil || claims != 1 {
		t.Fatal("reservation count", claims, e)
	}
}

func TestInferenceBudgetHistoricalMaterialAndDiagnosticsReadOnly(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	now := time.Now()
	o, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now)
	if e != nil {
		t.Fatal(e)
	}
	o.Material.InferenceBudget = nil
	o.MaterialDigest = domain.AcceptanceDigest(o.Material)
	raw, _ := json.Marshal(o)
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	doc["inference_diagnostic"] = "raw malicious diagnostic"
	raw, _ = json.Marshal(doc)
	if _, e = s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetModelAcceptance(a.ID, o.ID)
	if e != nil || got.InferenceDiagnostic != nil || got.MaterialDigest != o.MaterialDigest {
		t.Fatal("historical projection changed", e)
	}
	if _, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now); e == nil {
		t.Fatal("historical unbound approved")
	}
	var after []byte
	s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&after)
	if string(raw) != string(after) {
		t.Fatal("historical bytes rewritten")
	}
}

func TestInferenceBudgetLegacyRowsNeverRewrittenOrFinalized(t *testing.T) {
	for _, state := range []string{"pending", "running", "succeeded"} {
		t.Run(state, func(t *testing.T) {
			s, a, r, c, admission := inferenceLedgerFixture(t)
			now := time.Now()
			o, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now)
			if e != nil {
				t.Fatal(e)
			}
			o.Material.InferenceBudget = nil
			o.MaterialDigest = domain.AcceptanceDigest(o.Material)
			o.State = state
			if state != "pending" {
				o.Attempts = 1
				o.ClaimSequence = 3
				o.Certainty = "unknown"
				o.StartedAt = now.Format(time.RFC3339Nano)
			}
			raw, _ := json.Marshal(o)
			if _, e = s.db.Exec(`UPDATE model_acceptance_operations SET state=?,data=? WHERE id=?`, state, raw, o.ID); e != nil {
				t.Fatal(e)
			}
			if e = s.RecoverModelAcceptance(now); e != nil {
				t.Fatal(e)
			}
			var after []byte
			s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&after)
			if string(after) != string(raw) {
				t.Fatal("legacy row rewritten by recovery")
			}
			if _, e = s.FinishProductionInference(o, admission, "unknown", "unknown", "transport_unknown", domain.InferenceEvidence{}, 0, 0, false, now); e == nil {
				t.Fatal("unbound legacy finalized")
			}
		})
	}
}

// JSON makes the pre-implementation RED a behavioral failure, not a missing-field compile error.
func boundInferenceProposal(t *testing.T, a domain.ModelAccount, r domain.ModelRoute, c domain.ModelAcceptanceOperation, m domain.Mission) domain.ModelAcceptanceProposal {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"phase": "inference", "route_id": r.ID, "expected_account_revision": a.Revision, "prerequisite_id": c.ID, "model": domain.OpenAIInferenceModel, "budget_mission_id": m.ID, "expected_mission_revision": m.Revision})
	var q domain.ModelAcceptanceProposal
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestInferenceBudgetRequiresExplicitTask(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	q := inferenceProposal(a, r, c)
	q.BudgetMissionID = ""
	q.ExpectedMissionRevision = 0
	if _, err := s.ProposeProductionInference(a.ID, q, admission, time.Now()); err == nil {
		t.Fatal("unmetered production inference accepted without explicit task budget")
	}
}

func TestInferenceBudgetLastAvailableCallStaysChargedAtCap(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	m, _ := s.GetMission("inference-budget-task")
	var err error
	now := time.Now()
	for range 4 {
		o, err := s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now)
		if err != nil {
			t.Fatal(err)
		}
		o, err = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
		if err != nil {
			t.Fatal(err)
		}
		o, err = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.ReserveProductionInferenceDispatch(o, admission, now); err != nil {
			t.Fatal(err)
		}
		if err = s.ValidateProductionInferenceDispatch(o, admission, now); err != nil {
			t.Fatal("owned final debit denied", err)
		}
		if _, err = s.FinishProductionInference(o, admission, "unknown", "unknown", "transport_unknown", domain.InferenceEvidence{}, 0, 0, false, now); err != nil {
			t.Fatal(err)
		}
		m, err = s.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || m.Budget.UsedCalls != 7 {
		t.Fatal("cap debit lost")
	}
	if _, err = s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now); err == nil {
		t.Fatal("new call allowed at cap")
	}
}

func TestInferenceBudgetClaimDebitsOriginalTaskExactlyOnce(t *testing.T) {
	s, a, r, c, admission := inferenceLedgerFixture(t)
	m := inferenceBudgetMission(t, s, a, r)
	original, _ := json.Marshal(m.ModelSnapshot)
	assertBudget := func(used int) {
		t.Helper()
		got, e := s.GetMission(m.ID)
		if e != nil || got.Budget.UsedCalls != used || got.Budget.MaxCalls != 7 || got.State != "blocked" {
			t.Fatalf("task budget/state: %+v %v", got.Budget, e)
		}
		raw, _ := json.Marshal(got.ModelSnapshot)
		if string(raw) != string(original) {
			t.Fatal("original snapshot changed")
		}
	}
	now := time.Now()
	o, e := s.ProposeProductionInference(a.ID, boundInferenceProposal(t, a, r, c, m), admission, now)
	if e != nil {
		t.Fatal(e)
	}
	assertBudget(3)
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
	if e != nil {
		t.Fatal(e)
	}
	assertBudget(3)
	approved := o
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
	if e != nil {
		t.Fatal(e)
	}
	assertBudget(4)
	if e = s.ReserveProductionInferenceDispatch(o, admission, now); e != nil {
		t.Fatal(e)
	}
	if e = s.ValidateProductionInferenceDispatch(o, admission, now); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ClaimModelAcceptance(a.ID, approved.ID, approved.Revision, approved.MaterialDigest, now); e == nil {
		t.Fatal("claim replay accepted")
	}
	if _, e = s.FinishProductionInference(o, admission, "unknown", "unknown", "transport_unknown", domain.InferenceEvidence{}, 0, 0, false, now); e != nil {
		t.Fatal(e)
	}
	assertBudget(4)
}
