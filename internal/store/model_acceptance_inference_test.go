package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"testing"
	"time"
)

func inferenceLedgerFixture(t *testing.T, ttl ...time.Duration) (*Store, domain.ModelAccount, domain.ModelRoute, domain.ModelAcceptanceOperation, domain.ProductionAdmission) {
	t.Helper()
	s, c, a, r, connection := catalogFixture(t, ttl...)
	now := time.Now()
	ca := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
	o, e := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, ca, now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ReserveProductionCatalogDispatch(o, ca, now); e != nil {
		t.Fatal(e)
	}
	o, e = c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "gpt-6.1-sol", DisplayName: "GPT"}}, now)
	if e != nil {
		t.Fatal(e)
	}
	inferenceBudgetMission(t, s, a, r)
	return s, a, r, o, domain.ProductionAdmission{Generation: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Profile: "openai_siwc_inference_v1"}
}
func inferenceProposal(a domain.ModelAccount, r domain.ModelRoute, c domain.ModelAcceptanceOperation) domain.ModelAcceptanceProposal {
	return domain.ModelAcceptanceProposal{Phase: "inference", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: c.ID, Model: "gpt-6.1-sol", BudgetMissionID: "inference-budget-task", ExpectedMissionRevision: 1}
}

func TestProductionInferenceExpiryAtEveryBoundary(t *testing.T) {
	for _, stage := range []string{"proposal", "approval", "claim", "reservation", "after lease", "finish"} {
		t.Run(stage, func(t *testing.T) {
			s, a, r, c, admission := inferenceLedgerFixture(t, 5*time.Minute)
			now := time.Now()
			expired := now.Add(6 * time.Minute)
			q := inferenceProposal(a, r, c)
			if stage == "proposal" {
				if _, e := s.ProposeProductionInference(a.ID, q, admission, expired); e == nil {
					t.Fatal("expired proposal")
				}
				return
			}
			o, e := s.ProposeProductionInference(a.ID, q, admission, now)
			if e != nil {
				t.Fatal(e)
			}
			materialExpiry, _ := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
			authExpiry, _ := time.Parse(time.RFC3339Nano, o.Material.Authorization.ExpiresAt)
			if !materialExpiry.Equal(authExpiry) {
				t.Fatal("material outlives authorization")
			}
			if stage == "approval" {
				if _, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", expired); e == nil {
					t.Fatal("expired approval")
				}
				return
			}
			o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "claim" {
				if _, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, expired); e == nil {
					t.Fatal("expired claim")
				}
				return
			}
			o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
			if e != nil {
				t.Fatal(e)
			}
			if stage == "reservation" {
				if e = s.ReserveProductionInferenceDispatch(o, admission, expired); e == nil {
					t.Fatal("expired reservation")
				}
				return
			}
			if e = s.ReserveProductionInferenceDispatch(o, admission, now); e != nil {
				t.Fatal(e)
			}
			if stage == "after lease" {
				if e = s.ValidateProductionInferenceDispatch(o, admission, expired); e == nil {
					t.Fatal("expired dispatch")
				}
				return
			}
			matched := true
			if e = s.BeginProductionInferenceSend(o, admission, now); e != nil {
				t.Fatal(e)
			}
			evidence := domain.InferenceEvidence{TerminalEvent: "response.completed", ResolvedModel: "gpt-6.1-sol", OutputMatched: &matched, OutputDigest: domain.AcceptanceDigest("FOUNDRY_INFERENCE_OK")}
			if _, e = s.FinishProductionInference(o, admission, "succeeded", "completed", "", evidence, 0, 0, false, now.Add(31*time.Second)); e == nil {
				t.Fatal("late success accepted")
			}
		})
	}
}

func TestProductionInferenceFrozenMaterialAndCatalogLineage(t *testing.T) {
	for _, change := range []string{"prompt", "prompt digest", "model", "method", "url", "byte limit", "deadline", "attempts", "catalog digest", "connection", "admission", "scope", "route", "account", "catalog source", "catalog model", "catalog result", "catalog connection"} {
		t.Run(change, func(t *testing.T) {
			s, a, r, c, admission := inferenceLedgerFixture(t)
			now := time.Now()
			if change == "catalog source" || change == "catalog model" || change == "catalog result" || change == "catalog connection" {
				switch change {
				case "catalog source":
					c.Source = "local_simulation"
				case "catalog model":
					c.Catalog = nil
				case "catalog result":
					c.ResultSnapshotDigest = "forged"
				case "catalog connection":
					c.Material.ConnectionID = "wrong"
				}
				c.MaterialDigest = domain.AcceptanceDigest(c.Material)
				raw, _ := json.Marshal(c)
				if _, e := s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, c.ID); e != nil {
					t.Fatal(e)
				}
				if _, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now); e == nil {
					t.Fatal("false catalog accepted")
				}
				return
			}
			o, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now)
			if e != nil {
				t.Fatal(e)
			}
			switch change {
			case "prompt":
				o.Material.InferencePrompt = "evil"
			case "prompt digest":
				o.Material.PromptDigest = "wrong"
			case "model":
				o.Material.Model = "other"
			case "method":
				o.Material.RequestMethod = "GET"
			case "url":
				o.Material.RequestURL = "https://evil.invalid"
			case "byte limit":
				o.Material.ResponseByteLimit++
			case "deadline":
				o.Material.DeadlineSeconds++
			case "attempts":
				o.Material.MaxAttempts++
			case "catalog digest":
				o.Material.CatalogDigest = domain.AcceptanceDigest("forged")
			case "connection":
				o.Material.ConnectionID = "other"
			case "admission":
				o.Material.ProductionAdmission.Profile = "openai_siwc_catalog_v1"
			case "scope":
				o.Material.RequestedScopes = []string{"extra"}
			case "route":
				o.Material.Route.ExtraUsage = "on"
			case "account":
				o.Material.Account.Enabled = false
			}
			o.MaterialDigest = domain.AcceptanceDigest(o.Material)
			raw, _ := json.Marshal(o)
			if _, e = s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, raw, o.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now); e == nil {
				t.Fatal("forged material approved")
			}
		})
	}
}

func TestProductionInferenceReceiptAuthorityAndEvidence(t *testing.T) {
	for _, change := range []string{"generic finish", "wrong admission", "wrong claim", "no reservation", "rejected without reservation", "replayed reservation", "missing terminal", "missing match", "bad digest", "bad model", "unbounded error", "unknown usage", "mismatched certainty", "success"} {
		t.Run(change, func(t *testing.T) {
			s, a, r, c, admission := inferenceLedgerFixture(t)
			now := time.Now()
			o, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now)
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ValidateProductionInferenceDispatch(o, admission, now); e == nil {
				t.Fatal("unreserved dispatch")
			}
			if change != "no reservation" && change != "rejected without reservation" {
				if e = s.ReserveProductionInferenceDispatch(o, admission, now); e != nil {
					t.Fatal(e)
				}
				if e = s.BeginProductionInferenceSend(o, admission, now); e != nil {
					t.Fatal(e)
				}
			}
			if change == "replayed reservation" {
				if e = s.ReserveProductionInferenceDispatch(o, admission, now); e == nil {
					t.Fatal("reservation replay")
				}
				return
			}
			if change == "generic finish" {
				if _, e = s.FinishModelAcceptance(a.ID, o.ID, o.Revision, "succeeded", "completed", "", nil, 0, 0, false, now); e == nil {
					t.Fatal("generic finish accepted production")
				}
				return
			}
			if change == "rejected without reservation" {
				if _, e = s.FinishProductionInference(o, admission, "failed", "rejected", "quota_exhausted", domain.InferenceEvidence{TerminalEvent: "response.failed"}, 0, 0, false, now); e == nil {
					t.Fatal("unreserved provider evidence accepted")
				}
				return
			}
			matched := false
			ev := domain.InferenceEvidence{TerminalEvent: "response.completed", ResolvedModel: "gpt-6.1-sol", OutputMatched: &matched, OutputDigest: domain.AcceptanceDigest("different")}
			certainty := "completed"
			input := int64(0)
			switch change {
			case "wrong admission":
				admission.Generation = "cccccccccccccccccccccccccccccccc"
			case "wrong claim":
				o.ClaimSequence++
			case "missing terminal":
				ev.TerminalEvent = ""
			case "missing match":
				ev.OutputMatched = nil
			case "bad digest":
				ev.OutputDigest = "not digest"
			case "bad model":
				ev.ResolvedModel = "other"
			case "unbounded error":
				ev.ProviderCode = "raw provider text"
			case "unknown usage":
				input = 1
			case "mismatched certainty":
				certainty = "unknown"
			}
			got, e := s.FinishProductionInference(o, admission, "succeeded", certainty, "", ev, input, 0, false, now)
			if change == "success" {
				if e != nil || got.State != "succeeded" || got.OutputMatched == nil || *got.OutputMatched {
					t.Fatalf("mismatch receipt: %+v %v", got, e)
				}
				return
			}
			if e == nil {
				t.Fatal("forged receipt accepted")
			}
		})
	}
}

type inferenceLedger interface {
	ProposeProductionInference(string, domain.ModelAcceptanceProposal, domain.ProductionAdmission, time.Time) (domain.ModelAcceptanceOperation, error)
}

func TestProductionInferenceIndependentLedger(t *testing.T) {
	s, c, a, r, connection := catalogFixture(t)
	ledger, ok := any(s).(inferenceLedger)
	if !ok {
		t.Fatal("independent inference ledger is missing")
	}
	now := time.Now()
	catalogAdmission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
	o, e := c.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, catalogAdmission, now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
	if e != nil {
		t.Fatal(e)
	}
	o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.ReserveProductionCatalogDispatch(o, catalogAdmission, now); e != nil {
		t.Fatal(e)
	}
	o, e = c.FinishProductionCatalog(o, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: "gpt-6.1-sol", DisplayName: "GPT"}}, now)
	if e != nil {
		t.Fatal(e)
	}
	m := inferenceBudgetMission(t, s, a, r)
	q := boundInferenceProposal(t, a, r, o, m)
	admission := domain.ProductionAdmission{Generation: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Profile: "openai_siwc_inference_v1"}
	pending, e := ledger.ProposeProductionInference(a.ID, q, admission, now)
	if e != nil {
		t.Fatal(e)
	}
	if pending.Source != "production_inference" || pending.LiveAccess != "inference_once" || pending.State != "pending" {
		t.Fatalf("wrong inference authority: %+v", pending)
	}
	if _, e = s.ClaimModelAcceptance(a.ID, pending.ID, pending.Revision, pending.MaterialDigest, now); e == nil {
		t.Fatal("pending claim allowed")
	}
	for _, model := range []string{"", "other"} {
		q.Model = model
		if _, e = ledger.ProposeProductionInference(a.ID, q, admission, now); e == nil {
			t.Fatal("unfrozen model allowed")
		}
	}
}
