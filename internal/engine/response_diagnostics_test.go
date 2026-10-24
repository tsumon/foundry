package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSIWCDiagnosticPersistsWithConsumedBudgetAndNoPartialEvidence(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"PRIVATE partial output\"}\n\n")
	}))
	defer server.Close()
	e, s, m, _ := inferenceEngineAt(t, server.URL+"/v1", "simulation", true)
	m = approveInferenceContext(t, s, m)
	resolver := &diagnosticResolver{}
	e.gateway = model.NewRouter(nil, resolver, model.RouterOptions{SimulationOrigins: []string{server.URL}})
	var output any
	if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Researcher, nil, &output); err == nil {
		t.Fatal("partial response accepted")
	}
	rows, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	v, err := s.GetInvocation(rows[0].ID)
	if err != nil || v.Status != "outcome_unknown" || v.ErrorCode != "response_incomplete" || v.Result != nil || v.UsageKnown || v.ModelReceipt == nil || v.ModelReceipt.ResponseDiagnostic.Reason != "missing_terminal" {
		t.Fatalf("lost diagnostic/failure boundary: %+v %v", v, err)
	}
	m, err = s.GetMission(m.ID)
	if err != nil || m.Budget.UsedCalls != 1 || !m.Budget.UsageUnknown || calls.Load() != 1 || resolver.reads != 1 {
		t.Fatalf("budget/retry changed: %+v calls=%d reads=%d %v", m.Budget, calls.Load(), resolver.reads, err)
	}
	raw, _ := json.Marshal(v)
	if strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("private error/partial output persisted")
	}
}

type diagnosticResolver struct{ reads int }

func (r *diagnosticResolver) ResolveModelCredential(context.Context, domain.RoleModelSelection) (model.CredentialLease, error) {
	r.reads++
	return model.CredentialLease{Secret: "synthetic-diagnostic-credential", Generation: 1}, nil
}
