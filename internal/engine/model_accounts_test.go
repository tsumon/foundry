package engine

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/mcptest"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

func bindSyntheticAccount(t *testing.T, s *store.Store) domain.ModelAccount {
	t.Helper()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic", Name: "Synthetic only", Provider: "custom", BaseURL: "http://127.0.0.1", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "fake-one", SecretRef: "foundry-model-account-fake-one"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishModelCredential("fake-one", "available")
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProfile("test")
	if err != nil {
		t.Fatal(err)
	}
	p.AccountID = a.ID
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetModelAccount(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func accountCreateRequest(m domain.Mission, mode string) CreateRequest {
	return CreateRequest{Title: m.Title, Goal: m.Goal, RepoPath: m.RepoPath, Spec: m.Spec, WorkspaceMode: mode}
}

func TestAccountCreateFreezesAndProductionPlanDeniesWithoutMutation(t *testing.T) {
	e, s, _, _, old := setup(t)
	bindSyntheticAccount(t, s)
	e.gateway = model.NewClient(nil)
	for _, mode := range []string{"", domain.WorkspaceModeGenerationsV1} {
		m, err := e.Create(context.Background(), accountCreateRequest(old, mode))
		if err != nil {
			t.Fatal(err)
		}
		if m.ModelSnapshot == nil || m.ModelSnapshot.Roles[domain.Worker].Profile.CredentialVersion != 1 {
			t.Fatalf("not frozen: %+v", m.ModelSnapshot)
		}
		if _, bound, err := domain.BaseWorkspaceMode(m.WorkspaceMode); err != nil || !bound {
			t.Fatalf("missing wrapper: %s", m.WorkspaceMode)
		}
		_, err = e.Plan(context.Background(), m.ID, m.Revision)
		if !errors.Is(err, model.ErrAccountInferenceDisabled) {
			t.Fatalf("plan=%v", err)
		}
		got, err := s.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Revision != m.Revision || got.State != "draft" || got.Budget.UsedCalls != 0 {
			t.Fatalf("denial mutated: %+v", got)
		}
	}
}

func TestAccountAuthorityPreservesOuterFence(t *testing.T) {
	e, s, _, _, old := setup(t)
	bindSyntheticAccount(t, s)
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	if m.ModelSnapshot == nil {
		t.Fatal("missing account snapshot")
	}
	base := filepath.Join(e.dir(m.ID), "base")
	original, err := authority(m, base, false)
	if err != nil {
		t.Fatal(err)
	}
	m.ModelSnapshot.Roles[domain.Worker] = m.ModelSnapshot.Roles[domain.Main]
	p := m.Profiles[domain.Worker]
	p.AccountRevision++
	m.Profiles[domain.Worker] = p
	if _, err = authority(m, base, false); err == nil {
		t.Fatal("tampered profile received authority")
	}
	if original == "" {
		t.Fatal("empty authority")
	}
}

func TestAccountWrapperCreationIsInputOnlyRejected(t *testing.T) {
	e, _, _, _, m := setup(t)
	for _, mode := range []string{"account_snapshot_v1", "generations_account_snapshot_v1", "generations_controlled_read_account_snapshot_v1", "generations_controlled_mcp_read_account_snapshot_v1"} {
		if _, err := e.Create(context.Background(), accountCreateRequest(m, mode)); err == nil {
			t.Fatalf("accepted client wrapper %s", mode)
		}
	}
}

// Newer/corrupt persisted fixtures bypass today's creation API deliberately.
func seedPersistedMission(t *testing.T, e *Engine, m domain.Mission) domain.Mission {
	t.Helper()
	if m.Revision < 1 {
		m.Revision = 1
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	db := fixtureSQL(t, e)
	if _, err = db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES(?,?,?,?,?)`, m.ID, m.Revision, m.State, m.UpdatedAt, raw); err != nil {
		t.Fatal(err)
	}
	return m
}

type syntheticAccountGateway struct {
	model.Gateway
	preflight func(domain.Profile) error
}

func (g syntheticAccountGateway) PreflightAccountProfile(_ context.Context, p domain.Profile) error {
	if g.preflight != nil {
		return g.preflight(p)
	}
	return nil
}

func TestAccountOrdinaryContextDoesNotAuthorizeWorker(t *testing.T) {
	e, s, g, _, old := setup(t, edit)
	bindSyntheticAccount(t, s)
	e.gateway = syntheticAccountGateway{Gateway: g}
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.context", func(m *domain.Mission) error {
		m.State = "planning"
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err = e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Worker, nil, &out); err == nil {
		t.Fatal("context authorized Worker")
	}
	got, _ := s.GetMission(m.ID)
	if got.Budget.UsedCalls != 0 || len(g.roles) != 0 {
		t.Fatal("unapproved Worker consumed budget or dispatched")
	}
}

func TestAccountDisableBetweenPreflightAndReservationIsAtomic(t *testing.T) {
	e, s, g, _, old := setup(t, `{"answer":"should not dispatch"}`)
	a := bindSyntheticAccount(t, s)
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.context", func(m *domain.Mission) error {
		m.State = "planning"
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.gateway = syntheticAccountGateway{Gateway: g, preflight: func(domain.Profile) error { a.Enabled = false; _, err := s.SaveModelAccount(a, a.Revision); return err }}
	before, _ := json.Marshal(m)
	events, _ := s.Events(0, 100)
	var out any
	if err = e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, nil, &out); !errors.Is(err, store.ErrModelAccountUnavailable) {
		t.Fatalf("interleaving=%v", err)
	}
	got, _ := s.GetMission(m.ID)
	after, _ := json.Marshal(got)
	afterEvents, _ := s.Events(0, 100)
	if string(before) != string(after) || len(events) != len(afterEvents) || len(g.roles) != 0 {
		t.Fatal("denial changed authority, budget, events, or dispatched")
	}
}

func TestAccountMixedRolesGateOnlyApplicableActions(t *testing.T) {
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		t.Run(string(role), func(t *testing.T) {
			e, s, _, _, old := setup(t, research, plan)
			bindSyntheticAccount(t, s)
			p, err := s.GetProfile("test")
			if err != nil {
				t.Fatal(err)
			}
			p.ID = "account-role"
			if err = s.PutProfile(p); err != nil {
				t.Fatal(err)
			}
			p.ID, p.AccountID = "test", ""
			if err = s.PutProfile(p); err != nil {
				t.Fatal(err)
			}
			if err = s.PutSettings(domain.Settings{DefaultProfileID: "test", RoleBindings: map[domain.Role]string{role: "account-role"}}); err != nil {
				t.Fatal(err)
			}
			m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
			if err != nil {
				t.Fatal(err)
			}
			// The ordinary fake Gateway deliberately lacks the account preflight contract.
			_, err = e.Plan(context.Background(), m.ID, m.Revision)
			if role == domain.Researcher || role == domain.Planner {
				if !errors.Is(err, model.ErrAccountInferenceDisabled) {
					t.Fatalf("planning account role allowed: %v", err)
				}
				got, _ := s.GetMission(m.ID)
				if got.Revision != m.Revision || got.Budget.UsedCalls != 0 {
					t.Fatal("denied planning mutated mission")
				}
				return
			}
			if err != nil {
				t.Fatalf("unrelated role blocked planning: %v", err)
			}
			m = wait(t, s, m.ID, "awaiting_approval")
			if role == domain.Main {
				_, err = e.Chat(context.Background(), m.ID, m.Revision, "question")
			} else {
				_, err = e.Approve(context.Background(), m.ID, m.Revision)
			}
			if !errors.Is(err, model.ErrAccountInferenceDisabled) {
				t.Fatalf("matching action=%v", err)
			}
			got, _ := s.GetMission(m.ID)
			if got.Revision != m.Revision || got.Budget.UsedCalls != m.Budget.UsedCalls {
				t.Fatal("denied action mutated mission")
			}
		})
	}
}

func TestAccountAllFormatsSyntheticWorkflowAndDisabledHistory(t *testing.T) {
	for _, base := range []string{"", domain.WorkspaceModeGenerationsV1, domain.WorkspaceModeControlledReadV1, domain.WorkspaceModeControlledMCPReadV2} {
		t.Run(base, func(t *testing.T) {
			first := research
			if base == domain.WorkspaceModeControlledReadV1 || base == domain.WorkspaceModeControlledMCPReadV2 {
				first = `{"kind":"final","research":{"summary":"Synthetic no claims","findings":[],"unknowns":[]}}`
			}
			e, s, g, _, old := setup(t, first, plan, edit, review, review)
			a := bindSyntheticAccount(t, s)
			e.gateway = syntheticAccountGateway{Gateway: g, preflight: func(p domain.Profile) error {
				if p.CredentialVersion != 1 {
					t.Error("task followed current credential")
				}
				return nil
			}}
			request := accountCreateRequest(old, base)
			if base == domain.WorkspaceModeControlledReadV1 || base == domain.WorkspaceModeControlledMCPReadV2 {
				request = controlledRequest(t, s, old)
			}
			if base == domain.WorkspaceModeControlledMCPReadV2 {
				fixture, err := mcptest.Start(context.Background(), s)
				if err != nil {
					t.Fatal(err)
				}
				defer fixture.Close()
				descriptor := fixture.Descriptor()
				e.options.LocalMCPTest = &descriptor
				request.WorkspaceMode = base
				request.ToolMode = domain.ToolModeControlledMCPReadV2
				view, err := e.LocalMCPTestSelection()
				if err != nil {
					t.Fatal(err)
				}
				request.MCPSelection = view.Selection
			}
			m, err := e.Create(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			frozenDigest := m.ModelSnapshot.Digest
			if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			if m.State != "awaiting_approval" {
				t.Fatalf("planning=%s %s", m.State, m.Error)
			}
			g.mu.Lock()
			roles := append([]domain.Role(nil), g.roles...)
			g.mu.Unlock()
			if len(roles) != 2 || roles[0] != domain.Researcher || roles[1] != domain.Planner {
				t.Fatalf("context phase invoked execution role: %v", roles)
			}
			a.Enabled = false
			a, err = s.SaveModelAccount(a, a.Revision)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(m)
			if _, err = e.Approve(context.Background(), m.ID, m.Revision); !errors.Is(err, store.ErrModelAccountUnavailable) {
				t.Fatalf("disabled approve=%v", err)
			}
			current, _ := s.GetMission(m.ID)
			raw, _ := json.Marshal(current)
			if string(raw) != string(before) {
				t.Fatal("disabled approval created execution authority")
			}
			if err = e.checkSnapshot(m); err != nil {
				t.Fatalf("disabled account corrupted history: %v", err)
			}
			a.Enabled = true
			a, err = s.SaveModelAccount(a, a.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "two", SecretRef: "foundry-model-account-fake-two"}, a.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishModelCredential("two", "available"); err != nil {
				t.Fatal(err)
			}
			if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			if m.State != "awaiting_delivery" {
				t.Fatalf("execution=%s %s", m.State, m.Error)
			}
			if m.ModelSnapshot.Digest != frozenDigest || m.Profiles[domain.Worker].CredentialVersion != 1 {
				t.Fatal("execution changed frozen selection")
			}
			a, err = s.GetModelAccount(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			a.Enabled = false
			if _, err = s.SaveModelAccount(a, a.Revision); err != nil {
				t.Fatal(err)
			}
			m, err = e.Deliver(context.Background(), m.ID, m.Revision)
			if err != nil {
				t.Fatalf("disabled account blocked local delivery: %v", err)
			}
			if _, err = e.ReadDelivery(m.ID, "patch"); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatalf("disabled historical recovery: %v", err)
			}
		})
	}
}

func TestAccountMalformedReopenFailsBeforeRecovery(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "future"}[future], func(t *testing.T) {
			e, s, g, _, old := setup(t)
			bindSyntheticAccount(t, s)
			request := controlledRequest(t, s, old)
			m, err := e.Create(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			m, err = e.resetGeneration(m)
			if err != nil {
				t.Fatal(err)
			}
			m, err = e.grantPlanningRead(m)
			if err != nil {
				t.Fatal(err)
			}
			if future {
				m.ModelSnapshot.SchemaVersion = 2
			} else {
				m.ModelSnapshot.Digest = "corrupt"
			}
			raw, _ := json.Marshal(m)
			db := fixtureSQL(t, e)
			if _, err = db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			events, _ := s.Events(0, 100)
			before, _ := s.GetMission(m.ID)
			recorder := &recoveryRecordingRunner{}
			e.options.Runner = recorder
			e = reopenResearch(t, e)
			if err = e.Recover(context.Background()); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
				t.Fatalf("recovery=%v", err)
			}
			after, err := e.s.GetMission(m.ID)
			if err != nil {
				t.Fatal("malformed history unreadable", err)
			}
			beforeBytes, _ := json.Marshal(before)
			afterBytes, _ := json.Marshal(after)
			afterEvents, _ := e.s.Events(0, 100)
			if string(beforeBytes) != string(afterBytes) || len(events) != len(afterEvents) || recorder.calls != 0 || len(g.roles) != 0 {
				t.Fatal("malformed recovery performed work")
			}
		})
	}
}
