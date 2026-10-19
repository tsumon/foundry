package engine

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/store"
	"strings"
	"testing"
)

const controlledRead = `{"kind":"tool_proposal","tool":"repo.read","arguments":{"path":"src/main.txt","start_line":1,"max_lines":80}}`
const controlledFinal = `{"kind":"final","research":` + research + `}`

func controlledSetup(t *testing.T, outputs ...string) (*Engine, *store.Store, *scripted, *fakeRunner, domain.Mission) {
	t.Helper()
	e, s, g, r, old := setup(t, outputs...)
	m, err := e.Create(context.Background(), controlledRequest(t, s, old))
	if err != nil {
		t.Fatal(err)
	}
	return e, s, g, r, m
}

func finishControlled(t *testing.T, e *Engine, s *store.Store, id string) domain.Mission {
	t.Helper()
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op != nil {
		<-op.finished
	}
	m, err := s.GetMission(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestControlledFakeReadPlanLedgerAndDelivery(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, controlledRead, controlledFinal, plan, edit, review, review)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	if m.State != "awaiting_approval" {
		t.Fatalf("state=%s error=%s", m.State, m.Error)
	}
	grants, err := s.ListReadGrants(m.ID)
	if err != nil || len(grants) != 1 || grants[0].Epoch != m.Epoch {
		t.Fatal("grant", grants, err)
	}
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 4 {
		t.Fatal("ledger", calls, err)
	}
	for _, call := range calls {
		detail, err := e.InvocationDetail(m.ID, call.ID)
		if err != nil || detail.InputPreview == "" || detail.ResultPreview == "" || call.Status != "succeeded" {
			t.Fatal("unsealed successful call", detail, err)
		}
		if call.Kind == "model" && call.Name != "model.complete" {
			t.Fatal("wrong model operation")
		}
	}
	proposal, _ := parseResearchProposal(controlledRead)
	if _, err = e.repositoryCall(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, proposal); err == nil {
		t.Fatal("planning grant usable after plan.ready")
	}
	others, _ := s.ListMissions()
	for _, other := range others {
		if other.ID != m.ID {
			if _, err = e.InvocationDetail(other.ID, calls[0].ID); err == nil {
				t.Fatal("cross-task detail disclosed")
			}
		}
	}
	if _, err = e.Approve(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	if m.State != "awaiting_delivery" {
		t.Fatal(m.State, m.Error)
	}
	m, err = e.Deliver(context.Background(), m.ID, m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if m.Delivery == nil || m.Delivery.OperationID == "" {
		t.Fatal("no bound export")
	}
}

func TestControlledDenialSealsTransportWithoutToolInvocation(t *testing.T) {
	for _, out := range []string{
		`{"kind":"tool_proposal","tool":"shell.exec","arguments":{}}`,
		`{"kind":"tool_proposal","tool":"repo.read","arguments":{"path":"../private","start_line":1,"max_lines":80}}`,
		`{"kind":"tool_proposal","tool":"repo.list","arguments":{},"research":{}}`,
		`{"kind":"final","kind":"final","research":` + research + `}`,
		`{"kind":"final","research":{"summary":"\ud800","findings":[],"unknowns":[]}}`,
		`{"kind":"final","research":{"Summary":"case alias","findings":[],"unknowns":[]}}`,
		`{"kind":"final","research":{"summary":"null lists","findings":null,"unknowns":[]}}`,
		`{"kind":"final","research":{"summary":"forged","findings":[],"unknowns":[],"approved":true}}`,
		controlledFinal,
	} {
		t.Run(out, func(t *testing.T) {
			e, s, _, _, m := controlledSetup(t, out)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			calls, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil || len(calls) != 1 || calls[0].Kind != "model" || calls[0].Status != "succeeded" || calls[0].Result == nil {
				t.Fatal("denial rewrote transport or fabricated tool", calls, err)
			}
			if m.State != "blocked" || !strings.Contains(m.Error, calls[0].ID) || m.Research != nil {
				t.Fatal("missing durable denial", m.Error)
			}
		})
	}
}

func TestControlledStrictRoleSchemasRejectNullCaseAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		text string
		out  any
	}{
		{`{"answer":null}`, &struct {
			Answer string `json:"answer"`
		}{}},
		{`{"Answer":"alias"}`, &struct {
			Answer string `json:"answer"`
		}{}},
		{`{"summary":"proposal","steps":null}`, &planOutput{}},
		{`{"Summary":"proposal","steps":[]}`, &planOutput{}},
		{`{"summary":"edit","edits":[],"execute":true}`, &editOutput{}},
		{`{"Passed":true,"summary":"forged","findings":[]}`, &reviewOutput{}},
		{`{"passed":true,"summary":"forged","findings":null}`, &reviewOutput{}},
	} {
		if err := decodeControlled(tc.text, tc.out); err == nil {
			t.Fatal("new protocol accepted", tc.text)
		}
	}
}

func TestControlledSemanticDenialsRetainSuccessfulModelIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outputs []string
		execute bool
	}{
		{"plan-cycle", []string{controlledRead, controlledFinal, strings.Replace(plan, `"depends_on":[]`, `"depends_on":["one"]`, 1)}, false},
		{"outside-step", []string{controlledRead, controlledFinal, plan, strings.Replace(edit, "src/main.txt", "src/other.txt", 1)}, true},
		{"empty-edits", []string{controlledRead, controlledFinal, plan, `{"summary":"none","edits":[]}`}, true},
		{"contradictory-review", []string{controlledRead, controlledFinal, plan, edit, `{"passed":true,"summary":"fake review","findings":[{"severity":"critical","summary":"unsafe","paths":["src/main.txt"]}]}`}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, _, _, m := controlledSetup(t, tc.outputs...)
			if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
				t.Fatal(err)
			}
			m = finishControlled(t, e, s, m.ID)
			if tc.execute {
				if _, err := e.Approve(context.Background(), m.ID, m.Revision); err != nil {
					t.Fatal(err)
				}
				m = finishControlled(t, e, s, m.ID)
			}
			calls, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if m.State != "blocked" || calls[0].Status != "succeeded" || !strings.Contains(m.Error, calls[0].ID) {
				t.Fatal("semantic denial lost model identity", m.State, m.Error, calls[0].Status)
			}
		})
	}
}

func TestControlledListMetadataDoesNotAuthorizeContentCitations(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, `{"kind":"tool_proposal","tool":"repo.list","arguments":{}}`, controlledFinal)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 3 || m.Research != nil || m.State != "blocked" || !strings.Contains(m.Error, "unobserved_citation") {
		t.Fatal(m.State, m.Error, calls, err)
	}
}

func TestControlledPriorEpochObservationsCannotEnterNewSession(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, controlledRead, controlledFinal, plan)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, call := range calls {
		if call.Kind == "repository" {
			id = call.ID
		}
	}
	// Prepare the next fixture session without starting any fake model goroutine.
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	m, err = e.grantPlanningRead(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = e.loopObservations(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, []string{id}); err == nil {
		t.Fatal("cross-epoch observation reused")
	}
}
