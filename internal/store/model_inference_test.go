package store

import (
	"foundry/internal/domain"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInferenceReceiptIsOutcomeOnlyAndDurable(t *testing.T) {
	s := openTestStore(t)
	m := inferenceMission(t, s)
	intent := inferenceInvocation(m)
	receipt := &domain.ModelCallReceipt{RequestID: "req-one", ResolvedModel: "returned-model", CredentialGeneration: 2, HTTPStatus: 429, ProviderCode: "rate_limit_exceeded", ProviderParam: "model", BodyShape: "error_object", RetryAfterSeconds: 12, Certainty: "rejected"}
	intent.ModelReceipt = receipt
	if _, _, err := s.BeginInvocation(intent, m.Revision); err == nil {
		t.Fatal("receipt accepted as intent")
	}
	intent.ModelReceipt = nil
	_, v, err := s.BeginInvocation(intent, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	usage := domain.CallUsage{ModelReceipt: receipt}
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "failed", nil, "rate_limited", usage); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInvocation(v.ID)
	if err != nil || !reflect.DeepEqual(got.ModelReceipt, receipt) {
		t.Fatalf("receipt lost: %+v %v", got, err)
	}
	clone := *receipt
	usage.ModelReceipt = &clone
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "failed", nil, "rate_limited", usage); err != nil {
		t.Fatal("semantic receipt replay failed", err)
	}
	clone.RequestID = "different"
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "failed", nil, "rate_limited", usage); err == nil {
		t.Fatal("receipt overwritten")
	}
}

func TestInferenceReceiptMigrationPreservesExistingLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-ledger.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m, binding := controlledStoreMission(t, s, "old-ledger")
	m, g := controlledGrant(t, s, m, binding)
	_, v, err := s.BeginInvocation(modelInvocation("old-call", m, g, binding), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	output := controlledEnvelope("model_output", v.ID, m.ID, binding, `{}`)
	old, err := s.FinishInvocation(v.ID, v.Epoch, "succeeded", &output, "", domain.CallUsage{UsageKnown: true, InputTokens: 4, OutputTokens: 2})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the prior invocation table shape, leaving its actual historical
	// rows, constraints and triggers intact, then run the production migration.
	for _, q := range []string{`DROP TRIGGER invocation_no_initial_receipt`, `DROP TRIGGER invocation_receipt_outcome`, `ALTER TABLE invocations DROP COLUMN model_receipt`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetInvocation(v.ID)
	if err != nil || !reflect.DeepEqual(got, old) {
		t.Fatalf("migration changed historical receipt: %+v %v", got, err)
	}
	if _, err = s.db.Exec(`UPDATE invocations SET role='worker' WHERE id='old-call'`); err == nil {
		t.Fatal("migration lost immutable intent")
	}
	var integrity string
	if err = s.db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration foreign key violation")
	}
}

func TestInferenceReceiptRejectsUnsafeMetadataAndContradictoryOutcome(t *testing.T) {
	for _, receipt := range []domain.ModelCallReceipt{
		{Certainty: "rejected", RequestID: "raw secret with spaces"},
		{Certainty: "rejected", ProviderCode: "provider-body-arbitrary"},
		{Certainty: "rejected", BodyShape: "raw error text"},
		{Certainty: "rejected", HTTPStatus: 999},
		{Certainty: "rejected", RetryAfterSeconds: 999999},
		{Certainty: "completed"},
	} {
		t.Run(receipt.RequestID+receipt.ProviderCode+receipt.BodyShape+receipt.Certainty, func(t *testing.T) {
			s := openTestStore(t)
			m := inferenceMission(t, s)
			_, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishInvocation(v.ID, v.Epoch, "failed", nil, "auth_invalid", domain.CallUsage{ModelReceipt: &receipt}); err == nil {
				t.Fatal("unsafe/contradictory receipt saved")
			}
			got, _ := s.GetInvocation(v.ID)
			if got.Status != "dispatching" || got.ModelReceipt != nil {
				t.Fatal("rejected metadata persisted")
			}
		})
	}
}

func inferenceMission(t *testing.T, s *Store) domain.Mission {
	t.Helper()
	routeStoreFixture(t, s)
	a, _ := s.GetModelAccount("route-account")
	snapshotVersion(t, s, a, "inference-key")
	m := routedMission(t, s, "inference")
	m.Epoch = 1
	m.State = "planning"
	m.Approvals = []domain.Approval{{Kind: "context", Digest: strings.Repeat("b", 64)}}
	m, err := s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func inferenceInvocation(m domain.Mission) domain.Invocation {
	v := modelInvocation("inference-call", m, domain.ReadGrant{AuthorityDigest: strings.Repeat("b", 64)}, m.ModelSnapshot.Digest)
	v.Role = domain.Planner
	v.GrantID = ""
	return v
}

func TestInferenceOrdinaryReservationClaimFinish(t *testing.T) {
	s := openTestStore(t)
	m := inferenceMission(t, s)
	intent := inferenceInvocation(m)
	m, v, err := s.BeginInvocation(intent, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if m.Budget.UsedCalls != 1 {
		t.Fatal("reservation not charged once")
	}
	if _, _, err = s.BeginInvocation(intent, m.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err == nil {
		t.Fatal("second dispatcher claimed")
	}
	output := controlledEnvelope("model_output", v.ID, m.ID, v.BindingDigest, `{}`)
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &output, "", domain.CallUsage{}); err == nil {
		t.Fatal("schema2 outcome omitted provider receipt")
	}
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &output, "", domain.CallUsage{ModelReceipt: &domain.ModelCallReceipt{Certainty: "completed"}}); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.Budget.UsedCalls != 1 || !m.Budget.UsageUnknown {
		t.Fatal("budget refunded or usage invented")
	}
}

func TestInferenceAuthorityRejectsUnapprovedAndToolCalls(t *testing.T) {
	for _, kind := range []string{"missing-context", "worker", "binding", "repository", "researcher"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			m := inferenceMission(t, s)
			v := inferenceInvocation(m)
			switch kind {
			case "missing-context":
				m, _ = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error { m.Approvals = nil; return nil })
			case "worker":
				v.Role = domain.Worker
				m, _ = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error { m.State = "running"; return nil })
			case "binding":
				v.BindingDigest = strings.Repeat("c", 64)
				v.Input.BindingDigest = v.BindingDigest
			case "repository":
				v.Kind = "repository"
				v.Role = domain.Researcher
				v.Name = "repo.read"
			case "researcher":
				v.Role = domain.Researcher
			}
			_, _, err := s.BeginInvocation(v, m.Revision)
			if kind == "researcher" {
				if err != nil {
					t.Fatal("ordinary researcher incorrectly requires tool grant", err)
				}
			} else if err == nil {
				t.Fatal("unapproved call reserved")
			}
		})
	}
}

func TestInferenceClaimAndFinishRecheckLatestApproval(t *testing.T) {
	for _, phase := range []string{"claim", "finish"} {
		t.Run(phase, func(t *testing.T) {
			s := openTestStore(t)
			m := inferenceMission(t, s)
			m, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "finish" {
				if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.UpdateMission(m.ID, m.Revision, "fixture", func(m *domain.Mission) error {
				m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: strings.Repeat("c", 64)})
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if phase == "claim" {
				_, err = s.ClaimInvocation(v.ID, v.Epoch)
			} else {
				output := controlledEnvelope("model_output", v.ID, m.ID, v.BindingDigest, `{}`)
				_, err = s.FinishInvocation(v.ID, v.Epoch, "succeeded", &output, "", domain.CallUsage{ModelReceipt: &domain.ModelCallReceipt{Certainty: "completed"}})
			}
			if err == nil {
				t.Fatal("stale approval used")
			}
		})
	}
}

func TestInferenceCannotUseLegacyReservation(t *testing.T) {
	s := openTestStore(t)
	m := inferenceMission(t, s)
	if _, err := s.ReserveModelCall(m.ID, m.Revision, domain.Main); err == nil {
		t.Fatal("schema2 reserved without durable invocation")
	}
	got, _ := s.GetMission(m.ID)
	if got.Budget.UsedCalls != 0 {
		t.Fatal("legacy path consumed schema2 budget")
	}
}

func TestInferenceRuntimeAvailabilityRecheckedAtClaim(t *testing.T) {
	for _, change := range []string{"disable", "revoke", "route"} {
		t.Run(change, func(t *testing.T) {
			s := openTestStore(t)
			m := inferenceMission(t, s)
			m, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "disable":
				a, _ := s.GetModelAccount("route-account")
				a.Enabled = false
				_, err = s.SaveModelAccount(a, a.Revision)
			case "revoke":
				a, _ := s.GetModelAccount("route-account")
				_, err = s.RevokeModelCredential(a.ID, 1, a.Revision)
			case "route":
				_, err = s.db.Exec(`DROP TRIGGER model_route_no_update`)
				if err == nil {
					_, err = s.db.Exec(`UPDATE model_routes SET extra_usage='unknown' WHERE id='route'`)
				}
			}
			if err != nil {
				t.Fatal("fixture", err)
			}
			if _, err = s.ClaimInvocation(v.ID, v.Epoch); err == nil {
				t.Fatal("changed runtime authority claimed")
			}
			got, _ := s.GetMission(m.ID)
			if got.Budget.UsedCalls != 1 {
				t.Fatal("failed claim refunded")
			}
		})
	}
}

func TestInferenceOAuthClaimRechecksFrozenAuthorization(t *testing.T) {
	for _, change := range []string{"revoke", "refresh_unknown"} {
		t.Run(change, func(t *testing.T) {
			s := openTestStore(t)
			binding := authorizationFixture(t, s)
			route, err := s.CreateModelRoute(domain.ModelRoute{ID: "oauth-route", AccountID: binding.AccountID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.PutProfile(domain.Profile{ID: "oauth-profile", AccountID: binding.AccountID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "fake"}); err != nil {
				t.Fatal(err)
			}
			if err = s.PutSettings(domain.Settings{DefaultProfileID: "oauth-profile"}); err != nil {
				t.Fatal(err)
			}
			if _, err = s.BeginModelAuthorization(binding.AccountID, "login", "sign_in", binding.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.CommitModelAuthorization("login", binding, "2099-01-01T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
			m := routedMission(t, s, "oauth-inference")
			m.Epoch = 1
			m.State = "planning"
			m.Approvals = []domain.Approval{{Kind: "context", Digest: strings.Repeat("b", 64)}}
			m, err = s.CreateMission(m)
			if err != nil {
				t.Fatal(err)
			}
			_, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if change == "revoke" {
				_, err = s.RevokeModelAuthorization(binding.ID)
			} else {
				_, err = s.BeginModelAuthorization(binding.AccountID, "refresh", "refresh", binding.ID)
				if err == nil {
					err = s.FailModelAuthorization("refresh", "refresh_unknown")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimInvocation(v.ID, v.Epoch); err == nil {
				t.Fatal("unavailable frozen OAuth grant claimed")
			}
			after, err := s.GetMission(m.ID)
			if err != nil || after.ModelSnapshot.Digest != m.ModelSnapshot.Digest || after.Budget.UsedCalls != 1 {
				t.Fatal("availability changed history/budget", err)
			}
		})
	}
}
