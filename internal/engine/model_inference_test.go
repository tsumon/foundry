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

type inferenceGateway struct {
	requests      []model.Request
	result        model.Result
	err           error
	beforeReturn  func()
	beforeReserve func() error
}

func (g *inferenceGateway) PreflightModelSelection(context.Context, int, domain.RoleModelSelection) error {
	if g.beforeReserve != nil {
		return g.beforeReserve()
	}
	return nil
}
func (g *inferenceGateway) Complete(_ context.Context, r model.Request) (model.Result, error) {
	g.requests = append(g.requests, r)
	if g.beforeReturn != nil {
		g.beforeReturn()
	}
	return g.result, g.err
}

func inferenceEngine(t *testing.T) (*Engine, *store.Store, domain.Mission, *inferenceGateway) {
	return inferenceEngineAt(t, "http://127.0.0.1:1234/v1", "simulation")
}

type inferenceScriptGateway struct {
	*scripted
	t *testing.T
}

func TestInferenceLeavesLegacyControlledFailureContractUnchanged(t *testing.T) {
	e, s, _, _, m := controlledSetup(t)
	e.gateway = controlledGatewayFunc(func(context.Context, model.Request) (model.Result, error) {
		return model.Result{}, &model.ProviderError{Code: "auth_invalid", Certainty: "not_sent"}
	})
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	rows, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if rows[0].Status != "outcome_unknown" || rows[0].ErrorCode != "model_transport_unknown" || rows[0].ModelReceipt != nil {
		t.Fatalf("legacy outcome contract changed: %+v", rows[0])
	}
}

func (g inferenceScriptGateway) PreflightModelSelection(context.Context, int, domain.RoleModelSelection) error {
	return nil
}
func (g inferenceScriptGateway) Complete(ctx context.Context, r model.Request) (model.Result, error) {
	if r.SnapshotSchema != 2 || r.Selection == nil || r.Selection.Profile != r.Profile || r.Profile.CredentialVersion != 1 {
		g.t.Error("checkpoint lost exact frozen selection")
	}
	result, err := g.scripted.Complete(ctx, r)
	result.RequestID = "fixture-request"
	result.CredentialGeneration = 1
	return result, err
}

func TestInferenceAllWorkspaceFlowsPreserveAtomicCheckpointBudget(t *testing.T) {
	for _, base := range []string{"", domain.WorkspaceModeGenerationsV1, domain.WorkspaceModeControlledReadV1, domain.WorkspaceModeControlledMCPReadV2} {
		t.Run(base, func(t *testing.T) {
			e, s, old, _ := inferenceEngine(t)
			first := research
			request := accountCreateRequest(old, base)
			if base == domain.WorkspaceModeControlledReadV1 || base == domain.WorkspaceModeControlledMCPReadV2 {
				request = controlledRequest(t, s, old)
				first = `{"kind":"final","research":{"summary":"No claims","findings":[],"unknowns":[]}}`
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
			g := &scripted{outputs: []string{first, plan, edit, review, review}}
			e.gateway = inferenceScriptGateway{g, t}
			m, err := e.Create(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			if m.State != "awaiting_approval" || m.Budget.UsedCalls != 2 {
				t.Fatalf("planning checkpoint double charged/stalled: %s %s calls=%d", m.State, m.Error, m.Budget.UsedCalls)
			}
			if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			if m.State != "awaiting_delivery" || m.Budget.UsedCalls != 5 {
				t.Fatalf("execution stalled/charged wrong: %s %s calls=%d", m.State, m.Error, m.Budget.UsedCalls)
			}
			calls, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(calls) != 5 {
				t.Fatal("model calls not durable", calls, err)
			}
			for _, v := range calls {
				if v.Status != "succeeded" || v.ModelReceipt == nil || v.ModelReceipt.Certainty != "completed" {
					t.Fatal("missing provider receipt", v)
				}
			}
		})
	}
}
func inferenceEngineAt(t *testing.T, baseURL, environment string, subscription ...bool) (*Engine, *store.Store, domain.Mission, *inferenceGateway) {
	t.Helper()
	e, s, _, _, old := setup(t)
	account := domain.ModelAccount{ID: "inference-account", Name: "Synthetic", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: baseURL, Enabled: true}
	adapter, spend := "openai_api_responses", "explicit_api"
	if len(subscription) > 0 && subscription[0] {
		account.AuthMethod, account.BillingMode, adapter, spend = "openai_siwc", "subscription", "openai_siwc_responses", "subscription_only"
	}
	a, err := s.SaveModelAccount(account, 0)
	if err != nil {
		t.Fatal(err)
	}
	if adapter == "openai_siwc_responses" {
		if _, err = s.BeginModelAuthorization(a.ID, "fixture-sign-in", "sign_in", "fixture-auth"); err != nil {
			t.Fatal(err)
		}
		_, err = s.CommitModelAuthorization("fixture-sign-in", domain.ModelAuthorizationBinding{ID: "fixture-auth", AccountID: a.ID, Issuer: "https://auth.openai.com", Subject: "fixture", ClientID: "fixture-client", SpendClass: spend, Scopes: []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"}}, "2099-01-01T00:00:00Z")
		if err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "inference-key", SecretRef: "foundry-model-account-fake-inference-key"}, a.Revision); err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinishModelCredential("inference-key", "available"); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "inference-route", AccountID: a.ID, Adapter: adapter, Protocol: "responses", BaseURL: a.BaseURL, Environment: environment, SpendClass: spend, ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.GetProfile("test")
	p.AccountID, p.RouteID, p.BaseURL, p.Protocol = a.ID, r.ID, r.BaseURL, r.Protocol
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	g := &inferenceGateway{result: model.Result{Text: `{"answer":"recorded"}`, RequestID: "request-one", ResolvedModel: "actual-model", CredentialGeneration: 1}}
	e.gateway = g
	return e, s, m, g
}

func approveInferenceContext(t *testing.T, s *store.Store, m domain.Mission) domain.Mission {
	t.Helper()
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.context", func(m *domain.Mission) error {
		m.State = "planning"
		m.Epoch = 1
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInferenceEngineDurableInputOutputAndExactSelection(t *testing.T) {
	e, s, m, g := inferenceEngine(t)
	m = approveInferenceContext(t, s, m)
	var out struct {
		Answer string `json:"answer"`
	}
	if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, map[string]string{"question": "why"}, &out); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(rows) != 1 {
		t.Fatalf("missing durable call: %+v %v", rows, err)
	}
	v := rows[0]
	if v.Status != "succeeded" || v.Result == nil || v.ModelReceipt == nil || v.ModelReceipt.RequestID != "request-one" {
		t.Fatalf("missing result/receipt: %+v", v)
	}
	raw, err := e.evidenceStore().Read(v.Input)
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		SnapshotSchema int                        `json:"snapshot_schema"`
		Selection      *domain.RoleModelSelection `json:"selection"`
	}
	if err = json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	if input.SnapshotSchema != 2 || input.Selection == nil || input.Selection.Route.ID != "inference-route" || len(g.requests) != 1 || g.requests[0].SnapshotSchema != 2 || g.requests[0].Selection == nil || g.requests[0].Selection.Profile != m.Profiles[domain.Main] {
		t.Fatal("selection not sealed and transported exactly")
	}
	m, _ = s.GetMission(m.ID)
	if m.Budget.UsedCalls != 1 || !m.Budget.UsageUnknown {
		t.Fatal("reservation/unknown usage incorrect")
	}
}

func TestInferenceEngineFailuresAndMalformedRoleSeal(t *testing.T) {
	for _, tc := range []struct{ name, certainty, status string }{{"auth_invalid", "not_sent", "failed"}, {"rate_limited", "rejected", "failed"}, {"response_incomplete", "unknown", "outcome_unknown"}, {"malformed", "completed", "succeeded"}} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, m, g := inferenceEngine(t)
			m = approveInferenceContext(t, s, m)
			g.result.Text = "not role JSON"
			if tc.name != "malformed" {
				g.err = &model.ProviderError{Code: tc.name, Certainty: tc.certainty, RequestID: "failure-request", HTTPStatus: 429, ProviderCode: "rate_limit_exceeded", RetryAfterSeconds: 9}
			}
			var out struct {
				Answer string `json:"answer"`
			}
			if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, nil, &out); err == nil {
				t.Fatal("failed role accepted")
			}
			rows, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(rows) != 1 {
				t.Fatalf("missing receipt: %+v %v", rows, err)
			}
			if rows[0].Status != tc.status || rows[0].ModelReceipt == nil || rows[0].ModelReceipt.Certainty != tc.certainty {
				t.Fatalf("outcome %+v", rows[0])
			}
			if tc.name == "malformed" {
				raw, err := e.evidenceStore().Read(*rows[0].Result)
				if err != nil || string(raw) != "not role JSON" {
					t.Fatal("successful raw output lost", err)
				}
			} else if rows[0].Result != nil || rows[0].ErrorCode != tc.name {
				t.Fatal("partial output or unsafe failure")
			}
		})
	}
}

func TestInferenceEngineRequiresContextAndExecuteBeforeCall(t *testing.T) {
	e, s, m, g := inferenceEngine(t)
	for _, role := range []domain.Role{domain.Main, domain.Worker} {
		var out any
		if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, role, nil, &out); err == nil {
			t.Fatal("unapproved call")
		}
	}
	m, _ = s.GetMission(m.ID)
	if m.Budget.UsedCalls != 0 || len(g.requests) != 0 {
		t.Fatal("unapproved dispatch")
	}
	e.gateway = model.NewClient(nil)
	if err := e.preflightModelRoles(context.Background(), m, domain.Main); !errors.Is(err, model.ErrAccountInferenceDisabled) {
		t.Fatal(err)
	}
}

func TestInferenceDisableAfterPreflightCannotReserve(t *testing.T) {
	e, s, m, g := inferenceEngine(t)
	m = approveInferenceContext(t, s, m)
	g.beforeReserve = func() error {
		a, err := s.GetModelAccount("inference-account")
		if err != nil {
			return err
		}
		a.Enabled = false
		_, err = s.SaveModelAccount(a, a.Revision)
		return err
	}
	var output any
	if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, nil, &output); !errors.Is(err, store.ErrModelAccountUnavailable) {
		t.Fatal(err)
	}
	after, _ := s.GetMission(m.ID)
	rows, _ := s.ListInvocations(m.ID, 0, 100)
	if after.Revision != m.Revision || after.Budget.UsedCalls != 0 || len(rows) != 0 || len(g.requests) != 0 {
		t.Fatal("availability race acquired dispatch authority")
	}
}

func TestInferenceCorruptSnapshotStopsBeforeRecoveryMutation(t *testing.T) {
	for _, change := range []string{"route", "future"} {
		t.Run(change, func(t *testing.T) {
			e, s, m, g := inferenceEngine(t)
			m = approveInferenceContext(t, s, m)
			if change == "future" {
				m.ModelSnapshot.SchemaVersion = 99
			} else {
				m.ModelSnapshot.Roles[domain.Main].Route.BaseURL = "http://127.0.0.1:9999/v1"
			}
			raw, _ := json.Marshal(m)
			if _, err := fixtureSQL(t, e).Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			before, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			events, _ := s.Events(0, 100)
			runner := &recoveryRecordingRunner{}
			e.options.Runner = runner
			e = reopenResearch(t, e)
			if err = e.Recover(context.Background()); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
				t.Fatal(err)
			}
			after, err := e.s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			more, _ := e.s.Events(0, 100)
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) || len(events) != len(more) || runner.calls != 0 || len(g.requests) != 0 {
				t.Fatal("diagnostic recovery mutated state or called dependency")
			}
		})
	}
}

func TestInferenceRecoveryFencesEveryOrdinaryCallWithoutReplay(t *testing.T) {
	for _, dispatched := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "dispatching"}[dispatched], func(t *testing.T) {
			e, s, m, g := inferenceEngine(t)
			m = approveInferenceContext(t, s, m)
			m, err := s.UpdateMission(m.ID, m.Revision, "fixture.idle", func(m *domain.Mission) error { m.State = "awaiting_approval"; return nil })
			if err != nil {
				t.Fatal(err)
			}
			op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
			m, binding, auth, err := e.controlledAuthority(op, domain.Main)
			if err != nil {
				t.Fatal(err)
			}
			v, err := e.invocationIntent(m, domain.Main, "model", "model.complete", binding, auth)
			if err != nil {
				t.Fatal(err)
			}
			input, err := e.controlledModelInput(m, domain.Main, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(input)
			v.Input, err = e.seal(v, "model_input", raw, "application/json", false)
			if err != nil {
				t.Fatal(err)
			}
			_, v, err = s.BeginInvocation(v, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			if dispatched {
				if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
					t.Fatal(err)
				}
			}
			e = reopenResearch(t, e)
			for attempt := 0; attempt < 2; attempt++ {
				if err = e.Recover(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.s.GetInvocation(v.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "interrupted_before_dispatch"
			if dispatched {
				want = "outcome_unknown"
			}
			after, _ := e.s.GetMission(m.ID)
			if got.Status != want || got.ModelReceipt == nil || after.State != "blocked" || after.Epoch != m.Epoch+1 || after.Budget.UsedCalls != 1 || len(g.requests) != 0 {
				t.Fatalf("recovery replay/refund/unfenced/missing receipt: status=%s receipt=%+v state=%s calls=%d", got.Status, got.ModelReceipt, after.State, after.Budget.UsedCalls)
			}
		})
	}
}

func TestInferenceInterruptedOrUnsealedResultIsUnknown(t *testing.T) {
	for _, failure := range []string{"cancel", "cancel-provider-error", "seal"} {
		t.Run(failure, func(t *testing.T) {
			e, s, m, g := inferenceEngine(t)
			m = approveInferenceContext(t, s, m)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			trace := domain.HTTPExchangeDiagnostic{Version: 1, ClientRequestID: "fdy_0123456789abcdef0123456789abcdef", ProxyMode: "disabled", PeerClass: "loopback", Headers: domain.HTTPPhaseDiagnostic{State: "completed", Attempts: 1}, FirstByteMillis: 0, FinalStatus: 200, RequestWrites: 1, HeaderFields: 1, Transport: domain.HTTPTransportDiagnostic{Version: 1, ContentType: "single", Protocol: "http1_1", BodyLength: "positive"}}
			g.result.ExchangeDiagnostic = trace
			if failure == "cancel" || failure == "cancel-provider-error" {
				g.beforeReturn = cancel
				if failure == "cancel-provider-error" {
					g.result.RequestID = ""
					g.err = &model.ProviderError{Code: "response_incomplete", Certainty: "unknown", RequestID: "request-one", HTTPStatus: 200, ExchangeDiagnostic: trace}
				}
			} else {
				g.result.Text = "invalid\x00evidence"
			}
			var out any
			if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: ctx}, domain.Main, nil, &out); err == nil {
				t.Fatal("invalid success accepted")
			}
			rows, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			if rows[0].Status != "outcome_unknown" || rows[0].Result != nil || rows[0].ModelReceipt == nil || rows[0].ModelReceipt.Certainty != "unknown" || rows[0].ModelReceipt.RequestID != "request-one" {
				t.Fatalf("unknown receipt missing: %+v", rows[0])
			}
			stored, err := s.GetInvocation(rows[0].ID)
			if err != nil || stored.ModelReceipt == nil || stored.ModelReceipt.ExchangeDiagnostic != trace {
				t.Fatal("interrupted receipt lost exchange observation", err)
			}
		})
	}
}

func TestInferenceEveryRoleIncludingMixedLegacyIsDurable(t *testing.T) {
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		t.Run(string(role), func(t *testing.T) {
			e, s, old, g := inferenceEngine(t)
			if err := s.PutProfile(domain.Profile{ID: "legacy-role", Protocol: "chat_completions", BaseURL: "http://127.0.0.1", Model: "legacy"}); err != nil {
				t.Fatal(err)
			}
			if err := s.PutSettings(domain.Settings{DefaultProfileID: "test", RoleBindings: map[domain.Role]string{role: "legacy-role"}}); err != nil {
				t.Fatal(err)
			}
			m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
			if err != nil {
				t.Fatal(err)
			}
			m = approveInferenceContext(t, s, m)
			if role == domain.Worker || role == domain.Debugger || role == domain.QA || role == domain.Auditor {
				var output planOutput
				if err = json.Unmarshal([]byte(plan), &output); err != nil {
					t.Fatal(err)
				}
				p := output.pendingPlan()
				p.Revision = 1
				m.Plan = &p
				m.Spec.ImageID = "sha256:fixture"
				auth, err := authority(m, filepath.Join(e.dir(m.ID), "base"), true)
				if err != nil {
					t.Fatal(err)
				}
				m, err = s.UpdateMission(m.ID, m.Revision, "fixture.plan", func(current *domain.Mission) error {
					current.Plan = m.Plan
					current.Spec.ImageID = m.Spec.ImageID
					current.State = "awaiting_approval"
					current.Approvals = append(current.Approvals, domain.Approval{Kind: "plan_proposal", Digest: auth, PlanRevision: 1})
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				m, err = s.ApproveMissionExecution(m.ID, m.Revision, auth)
				if err != nil {
					t.Fatal(err)
				}
				if role != domain.Worker {
					m, err = s.UpdateMission(m.ID, m.Revision, "fixture.verifying", func(m *domain.Mission) error { m.State = "verifying"; return nil })
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			var out struct {
				Answer string `json:"answer"`
			}
			if err = e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, role, nil, &out); err != nil {
				t.Fatal(err)
			}
			rows, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(rows) != 1 || rows[0].Role != role || rows[0].ModelReceipt == nil || len(g.requests) != 1 || g.requests[0].Selection.Account != nil {
				t.Fatal("mixed legacy bypassed durable path", rows, err)
			}
		})
	}
}
