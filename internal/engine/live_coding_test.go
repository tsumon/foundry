package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type codingLease struct {
	s     *store.Store
	reads int
}

func newCodingSyntheticCapability(d *sandbox.C4SyntheticDriver, ready func(context.Context) (sandbox.C4ReadinessResult, error)) *C4ControlledCapability {
	return &C4ControlledCapability{modelBacked: true, config: C4Activation{SchemaVersion: 1, Enabled: true, RuntimeID: "owned-runtime", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}, source: "synthetic_c4_control", driver: d, readiness: ready}
}
func (l *codingLease) ResolveModelCredential(ctx context.Context, s domain.RoleModelSelection) (model.CredentialLease, error) {
	l.reads++
	return model.CredentialLease{Secret: "owned-local-credential", Generation: 1}, nil
}

func codingImport(t *testing.T, base string) (*Engine, *store.Store, CreateRequest, *c4Driver, *codingLease) {
	t.Helper()
	return codingImportWithStore(t, base, nil)
}

func codingImportWithStore(t *testing.T, base string, persistent *store.Store) (*Engine, *store.Store, CreateRequest, *c4Driver, *codingLease) {
	return codingImportWithAdapter(t, base, persistent, "openai_api_responses")
}

func codingImportWithAdapter(t *testing.T, base string, persistent *store.Store, adapter string) (*Engine, *store.Store, CreateRequest, *c4Driver, *codingLease) {
	t.Helper()
	e, s, repo, _ := importFixture(t)
	if persistent != nil {
		s, e.s = persistent, persistent
	}
	for name, body := range map[string]string{"main.go": "package demo\nfunc Label() string { return \"old\" }\n", "main_test.go": "package demo\n", "go.mod": "module demo\n\ngo 1.27.3\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	importGit(t, repo, "add", ".")
	importGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "owned test repository")
	provider := "openai"
	if adapter == "compatible_api_responses" {
		provider = "custom"
	}
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "coding-account", Name: "Owned simulation", Provider: provider, AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: base, Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "coding-key", SecretRef: "foundry-model-account-owned-coding-key"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishModelCredential("coding-key", "available"); err != nil {
		t.Fatal(err)
	}
	route, err := s.CreateModelRoute(domain.ModelRoute{ID: "coding-route", AccountID: a.ID, Adapter: adapter, Protocol: "responses", BaseURL: base, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutProfile(domain.Profile{ID: "coding-profile", Name: "Owned model", Kind: "openai", AccountID: a.ID, RouteID: route.ID, BaseURL: base, Protocol: "responses", Model: "owned-model"}); err != nil {
		t.Fatal(err)
	}
	v, err := e.InspectRepository(context.Background(), domain.RepositoryInspectionRequest{RepoPath: repo, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"main.go"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "c4-local"}})
	if err != nil || v.Status != "ready" {
		t.Fatal(v, err)
	}
	profile := *c4Profile()
	profile.ID = "c4_go_model_v1"
	profile.ControlVersion = 1
	d := &c4Driver{}
	cap := newCodingSyntheticCapability(sandbox.NewC4SyntheticDriver(d), func(context.Context) (sandbox.C4ReadinessResult, error) {
		return sandbox.C4ReadinessResult{Fingerprint: strings.Repeat("c", 64)}, nil
	})
	e.options.C4Controlled = cap
	lease := &codingLease{s: s}
	e.gateway = model.NewRouter(nil, lease, model.RouterOptions{SimulationOrigins: []string{strings.TrimSuffix(base, "/v1")}, CodingAdmission: e.AdmitCodingModelCall})
	cap.models = e.gateway
	return e, s, CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: v.ID, Digest: v.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "Model coding", Goal: "Change Label to ready", RepoPath: v.Root, Spec: domain.Spec{Scope: v.Spec.Scope, Acceptance: v.Spec.Acceptance, Image: v.Spec.Image, ProfileID: "coding-profile", Research: true, MaxCalls: 16, C4: &profile}}, d, lease
}

func TestLiveCodingOwnedRouterRepairDelivery(t *testing.T) {
	testCodingOwnedRouterRepairDelivery(t, "openai_api_responses")
}

func TestCompatibleCodingOwnedRouterRepairDelivery(t *testing.T) {
	testCodingOwnedRouterRepairDelivery(t, "compatible_api_responses")
}

func testCodingOwnedRouterRepairDelivery(t *testing.T, adapter string) {
	replies := []string{`{"summary":"observed","findings":[],"unknowns":[]}`, `{"summary":"change label","steps":[{"id":"label","title":"change label","description":"change implementation","depends_on":[],"files":["main.go"]}]}`, `{"summary":"first candidate","edits":[{"path":"main.go","content":"package demo // broken\n"}]}`, `{"summary":"repair","edits":[{"path":"main.go","content":"package demo\nfunc Label() string { return \"ready\" }\n"}]}`, codingPass, codingPass}
	wire := &codingReviewGateway{script: &scripted{outputs: replies}}
	requests := []model.Request{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Instructions string `json:"instructions"`
			Input        []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Input) != 1 {
			t.Error("invalid protocol")
			w.WriteHeader(400)
			return
		}
		roles := []domain.Role{domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor}
		if len(requests) >= len(roles) {
			t.Error("extra send")
			w.WriteHeader(500)
			return
		}
		q := model.Request{Role: roles[len(requests)], System: body.Instructions, Prompt: body.Input[0].Content}
		requests = append(requests, q)
		output, err := wire.Complete(r.Context(), q)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", fmt.Sprintf("owned-%d", len(requests)))
		json.NewEncoder(w).Encode(map[string]any{"object": "response", "id": "owned-response", "model": "owned-returned", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": output.Text}}}}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 5}})
	}))
	defer server.Close()
	e, s, q, d, lease := codingImportWithAdapter(t, server.URL+"/v1", nil, adapter)
	before := importSourceDigest(t, q.RepoPath)
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if lease.reads != 0 || d.creates != 0 || len(m.Approvals) != 1 {
		t.Fatal("creation performed external effects/approval")
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err == nil {
		t.Fatal("planned without baseline")
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("missing context consent accepted")
	}
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_approval" {
		t.Fatal(m.State, m.Error)
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	finishCoding(e)
	m, _ = s.GetMission(m.ID)
	if m.State != "awaiting_delivery" {
		t.Fatal(m.State, m.Error)
	}
	if len(m.C4Operations) != 3 || m.C4Operations[1].Result.Class != "test_failed" || !m.C4Operations[2].Passed() || m.C4Operations[1].CandidateDigest == m.C4Operations[2].CandidateDigest {
		t.Fatal("missing repair generations", m.C4Operations)
	}
	if len(m.Changes) != 2 || m.Changes[0].Before == "" || !strings.Contains(m.Changes[1].After, "ready") {
		t.Fatal("lost full changes", m.Changes)
	}
	if !strings.Contains(requests[3].Prompt, "broken") || !strings.Contains(requests[4].Prompt, "ready") {
		t.Fatal("stale candidate prompts")
	}
	invocations, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(invocations) != 6 {
		t.Fatal(invocations, err)
	}
	for _, v := range invocations {
		if v.Status != "succeeded" || v.ModelReceipt == nil || v.ModelReceipt.RequestID == "" || v.Input.Digest == "" {
			t.Fatal("missing authority/receipt", v)
		}
	}
	if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	report, err := e.ReadDelivery(m.ID, "report")
	if err != nil || !strings.Contains(string(report), "owned-6") || !strings.Contains(string(report), "owned-returned") || !strings.Contains(string(report), "simulation") {
		t.Fatal("delivery missing distinct model provenance", err)
	}
	patch, err := e.ReadDelivery(m.ID, "patch")
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	deliveredRevision, deliveredEpoch := m.Revision, m.Epoch
	if _, err = e.PrepareC4Readiness(context.Background(), m.ID, m.Revision); err == nil {
		t.Fatal("delivered task accepted readiness mutation")
	}
	if _, err = e.Chat(context.Background(), m.ID, m.Revision, "explain delivered patch"); err == nil {
		t.Fatal("delivered task accepted a new model call")
	}
	m, _ = s.GetMission(m.ID)
	if m.Revision != deliveredRevision || m.Epoch != deliveredEpoch || m.State != "delivered" {
		t.Fatal("denied post-delivery action mutated task")
	}
	// A new runtime has no readiness authority, but still must read the exact
	// sealed delivery without refreshing controls or replaying a Main call.
	e.options.C4Controlled.config.RuntimeID = "restarted-after-delivery"
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	afterReport, reportErr := e.ReadDelivery(m.ID, "report")
	afterPatch, patchErr := e.ReadDelivery(m.ID, "patch")
	m, _ = s.GetMission(m.ID)
	if reportErr != nil || patchErr != nil || string(afterReport) != string(report) || string(afterPatch) != string(patch) || m.Revision != deliveredRevision || m.Epoch != deliveredEpoch {
		t.Fatal("delivered artifact was not stable", reportErr, patchErr)
	}
	if before != importSourceDigest(t, q.RepoPath) || lease.reads != 6 || d.creates != 3 || d.removes != 3 {
		t.Fatal("source/effect mismatch")
	}
	e.options.C4Controlled.config.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	if e.c4ControlledGate(m, true) == nil {
		t.Fatal("expired runtime admitted")
	}
}

func TestCodingAdmissionRejectsChangedOrUnclaimedAuthorityBeforeEffects(t *testing.T) {
	for _, kind := range []string{"unclaimed", "prompt", "role", "snapshot", "approval", "epoch", "expiry", "readiness", "restart"} {
		t.Run(kind, func(t *testing.T) {
			e, s, q, d, lease := codingImport(t, "http://127.0.0.1:1/v1")
			m, err := e.Create(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			m = controlReady(t, e, m)
			m = c4Baseline(t, e, m)
			m, err = e.resetGeneration(m)
			if err != nil {
				t.Fatal(err)
			}
			op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
			m, binding, auth, err := e.controlledAuthority(op, domain.Planner)
			if err != nil {
				t.Fatal(err)
			}
			input, err := e.controlledModelInput(m, domain.Planner, nil)
			if err != nil {
				t.Fatal(err)
			}
			v, err := e.invocationIntent(m, domain.Planner, "model", "model.complete", binding, auth)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(input)
			if kind != "unclaimed" {
				v, err = e.reserveClaim(m, v, raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			request := model.Request{InvocationID: v.ID, SnapshotSchema: input.SnapshotSchema, Selection: input.Selection, Profile: input.Profile, Role: input.Role, System: input.System, Prompt: input.Prompt, MaxOutputTokens: input.MaxOutputTokens}
			switch kind {
			case "prompt":
				request.Prompt += "changed"
			case "role":
				request.Role = domain.Worker
			case "snapshot":
				request.Selection.Profile.Model = "changed"
			case "expiry":
				e.options.C4Controlled.config.ExpiresAt = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
			case "readiness":
				e.c4Clock = func() time.Time { return time.Now().Add(20 * time.Minute) }
			case "restart":
				e.options.C4Controlled.config.RuntimeID = "new-process"
			case "approval", "epoch":
				m, _ = s.GetMission(m.ID)
				_, err = s.UpdateMission(m.ID, m.Revision, "test.changed_authority", func(x *domain.Mission) error {
					if kind == "epoch" {
						x.Epoch++
					} else {
						x.Approvals = append(x.Approvals, domain.Approval{Kind: "context", Digest: strings.Repeat("f", 64)})
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = e.AdmitCodingModelCall(context.Background(), request); err == nil {
				t.Fatal("changed authority admitted", kind)
			}
			if lease.reads != 0 || d.creates != 1 {
				t.Fatal("admission had effects")
			}
		})
	}
}

func ownedCodingResponse(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "owned-response")
	json.NewEncoder(w).Encode(map[string]any{"object": "response", "id": "owned-response", "model": "owned-model", "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": text}}}}})
}

const ownedCodingPlan = `{"summary":"change label","steps":[{"id":"label","title":"change label","description":"change implementation","depends_on":[],"files":["main.go"]}]}`

// Provider/auth/unknown/invalid/protected failures must not become Debugger
// prompts. Only clean test_failed can consume the two bounded repair rounds.
func TestLiveCodingStopsUnsafeFailuresAndBoundsRepair(t *testing.T) {
	for _, kind := range []string{"auth", "quota", "unknown", "malformed", "protected", "cleanup", "repair_limit", "call_budget"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					ownedCodingResponse(w, ownedCodingPlan)
					return
				}
				switch kind {
				case "auth":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error":{"code":"invalid_api_key"}}`)
				case "quota":
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(429)
					fmt.Fprint(w, `{"error":{"code":"insufficient_quota"}}`)
				case "unknown":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"object":"response","status":"incomplete","output":[]}`)
				case "malformed":
					ownedCodingResponse(w, `{"summary":"missing edits"}`)
				case "protected":
					ownedCodingResponse(w, `{"summary":"rewrite test","edits":[{"path":"main_test.go","content":"package demo\n"}]}`)
				default:
					ownedCodingResponse(w, fmt.Sprintf(`{"summary":"broken %d","edits":[{"path":"main.go","content":"package demo // broken %d\n"}]}`, calls, calls))
				}
			}))
			defer server.Close()
			e, s, q, d, lease := codingImport(t, server.URL+"/v1")
			q.Spec.Research = false
			if kind == "call_budget" {
				q.Spec.MaxCalls = 2
			}
			m, err := e.Create(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			m = controlReady(t, e, m)
			m = c4Baseline(t, e, m)
			if kind == "cleanup" {
				d.cleanupFail = true
			}
			if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "awaiting_approval" {
				t.Fatal(m.State, m.Error)
			}
			if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
				t.Fatal(err)
			}
			finishCoding(e)
			m, _ = s.GetMission(m.ID)
			if m.State != "blocked" {
				t.Fatal("failure did not stop", m.State, m.Error)
			}
			wantCalls := 2
			wantCreates := 1
			if kind == "cleanup" || kind == "call_budget" {
				wantCreates = 2
			}
			if kind == "repair_limit" {
				wantCalls = 4
				wantCreates = 4
			}
			if calls != wantCalls || lease.reads != wantCalls || d.creates != wantCreates || m.Budget.UsedCalls != wantCalls {
				t.Fatalf("unexpected retry/budget/dispatch calls=%d reads=%d creates=%d budget=%d error=%s", calls, lease.reads, d.creates, m.Budget.UsedCalls, m.Error)
			}
			if _, err = e.Deliver(context.Background(), m.ID, m.Revision); err == nil {
				t.Fatal("delivered failure")
			}
			rows, _ := s.ListInvocations(m.ID, 0, 100)
			for _, v := range rows {
				if kind != "repair_limit" && v.Role == domain.Debugger {
					t.Fatal("unsafe failure entered repair")
				}
			}
			if kind == "unknown" && rows[0].Status != "outcome_unknown" {
				t.Fatal("unknown lost", rows[0])
			}
		})
	}
}

func TestCodingRecoveryConsumesClaimWithoutCredentialOrVerifierReplay(t *testing.T) {
	e, s, q, d, lease := codingImport(t, "http://127.0.0.1:1/v1")
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	op := &operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}
	m, binding, auth, err := e.controlledAuthority(op, domain.Planner)
	if err != nil {
		t.Fatal(err)
	}
	input, err := e.controlledModelInput(m, domain.Planner, nil)
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.invocationIntent(m, domain.Planner, "model", "model.complete", binding, auth)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(input)
	v, err = e.reserveClaim(m, v, raw)
	if err != nil {
		t.Fatal(err)
	}
	e.options.C4Controlled.config.RuntimeID = "restarted"
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInvocation(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if got.Status != "outcome_unknown" || m.State != "blocked" || m.Budget.UsedCalls != 1 || lease.reads != 0 || d.creates != 1 || d.removes != 1 {
		t.Fatal("recovery replay/refund", got, m.State, m.Error)
	}
	before := m.Revision
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.Revision != before || d.creates != 1 || lease.reads != 0 {
		t.Fatal("recovery not stable")
	}
}

func TestCodingPauseDuringOwnedHTTPRetainsUnknownClaim(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			return
		}
		close(entered)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	e, s, q, d, lease := codingImport(t, server.URL+"/v1")
	q.Spec.Research = false
	m, err := e.Create(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	if _, err = e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("owned call did not start")
	}
	m, _ = s.GetMission(m.ID)
	if _, err = e.Pause(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 1 || calls[0].Status != "outcome_unknown" || m.Budget.UsedCalls != 1 || lease.reads != 1 || d.creates != 1 {
		t.Fatal("pause retried/refunded/lost claim", m.State, m.Error, calls, err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lease.reads != 1 || d.creates != 1 {
		t.Fatal("restart replayed canceled call")
	}
}
