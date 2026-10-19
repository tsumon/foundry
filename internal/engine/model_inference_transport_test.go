package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type claimedInferenceResolver struct {
	s         *store.Store
	missionID string
	reads     int
}

func (r *claimedInferenceResolver) ResolveModelCredential(_ context.Context, selection domain.RoleModelSelection) (model.CredentialLease, error) {
	r.reads++
	rows, err := r.s.ListInvocations(r.missionID, 0, 100)
	if err != nil || len(rows) != 1 || rows[0].Status != "dispatching" || selection.Profile.CredentialVersion != 1 {
		return model.CredentialLease{}, fmt.Errorf("secret access before exact durable claim")
	}
	return model.CredentialLease{Secret: "synthetic-task4-credential", Generation: 1}, nil
}

func TestInferenceLoopbackRouterClaimsBeforeSecretAndRecordsOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, status, code string
		http               int
		body               string
	}{
		{"complete", "succeeded", "", 200, `{"object":"response","id":"response-one","model":"returned-model","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"{\"answer\":\"safe\"}"}]}],"usage":{"input_tokens":4,"output_tokens":2}}`},
		{"rejected", "failed", "rate_limited", 429, `{"error":{"code":"rate_limit_exceeded","message":"untrusted error detail"}}`},
		{"incomplete", "outcome_unknown", "response_incomplete", 200, `{"object":"response","status":"incomplete","output":[]}`},
		{"secret-echo", "outcome_unknown", "response_incomplete", 200, `{"object":"response","status":"completed","output":[{"type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"synthetic-task4-credential"}]}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer synthetic-task4-credential" {
					t.Error("wrong exact route/credential")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "upstream-one")
				w.WriteHeader(tc.http)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			e, s, m, _ := inferenceEngineAt(t, server.URL+"/v1", "simulation")
			m = approveInferenceContext(t, s, m)
			resolver := &claimedInferenceResolver{s: s, missionID: m.ID}
			e.gateway = model.NewRouter(nil, resolver, model.RouterOptions{SimulationOrigins: []string{server.URL}})
			var output struct {
				Answer string `json:"answer"`
			}
			err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, nil, &output)
			if (err == nil) != (tc.status == "succeeded") {
				t.Fatal(err)
			}
			rows, readErr := s.ListInvocations(m.ID, 0, 100)
			if readErr != nil || len(rows) != 1 {
				t.Fatal(rows, readErr)
			}
			v := rows[0]
			if v.Status != tc.status || v.ErrorCode != tc.code || v.ModelReceipt == nil || v.ModelReceipt.RequestID != "upstream-one" || v.ModelReceipt.CredentialGeneration != 1 || calls.Load() != 1 || resolver.reads != 1 {
				t.Fatalf("bad durable routed outcome: %+v calls=%d reads=%d", v, calls.Load(), resolver.reads)
			}
			safe, _ := json.Marshal(v)
			if strings.Contains(string(safe), "synthetic-task4-credential") || strings.Contains(string(safe), "untrusted error detail") {
				t.Fatal("unsafe receipt")
			}
			m, _ = s.GetMission(m.ID)
			if m.Budget.UsedCalls != 1 {
				t.Fatal("incorrect charge")
			}
			if tc.status == "succeeded" && (!v.UsageKnown || v.InputTokens != 4 || v.OutputTokens != 2 || v.ModelReceipt.ResolvedModel != "returned-model") {
				t.Fatal("provider usage/model lost")
			}
		})
	}
}

func TestInferenceProductionGatePrecedesReservationAndSecret(t *testing.T) {
	e, s, m, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live")
	m = approveInferenceContext(t, s, m)
	resolver := &claimedInferenceResolver{s: s, missionID: m.ID}
	e.gateway = model.NewRouter(nil, resolver, model.RouterOptions{})
	var output any
	err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, nil, &output)
	if err == nil || err.Error() != "live_provider_access_pending_confirmation" {
		t.Fatal(err)
	}
	rows, _ := s.ListInvocations(m.ID, 0, 100)
	m, _ = s.GetMission(m.ID)
	if len(rows) != 0 || m.Budget.UsedCalls != 0 || resolver.reads != 0 {
		t.Fatal("live gate read secret/reserved")
	}
}
