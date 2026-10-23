package vmacceptance

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalFixtureRejectsChangedWireBeforeConsumingResponse(t *testing.T) {
	f := newLocalFixture()
	q := model.Request{InvocationID: "claimed-1", Role: domain.Researcher, System: "sealed-system", Prompt: `{"goal":"owned"}`, Profile: domain.Profile{Model: fixtureModel}, MaxOutputTokens: 8192}
	if err := f.authorize(q); err != nil {
		t.Fatal(err)
	}
	body := `{"model":"SYNTHETIC-LOCAL-PROTOCOL","instructions":"sealed-system","input":[{"role":"user","content":"changed"}],"max_output_tokens":8192,"store":false}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+fixtureCredential)
	w := httptest.NewRecorder()
	f.ServeHTTP(w, req)
	if w.Code != 400 || len(f.calls) != 0 {
		t.Fatal("changed wire accepted", w.Code, f.calls)
	}
}

func TestLocalFixtureOutputsOnlyScopedRepairAndBoundReview(t *testing.T) {
	for _, role := range []domain.Role{domain.Worker, domain.Debugger} {
		q := model.Request{Role: role}
		out, err := fixtureReply(q)
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Edits []struct{ Path, Content string }
		}
		if json.Unmarshal([]byte(out), &v) != nil || len(v.Edits) != 1 || v.Edits[0].Path != "triple.go" {
			t.Fatal("unscoped edit", out)
		}
		if role == domain.Debugger && v.Edits[0].Content != repairedSource {
			t.Fatal("repair not addition-only")
		}
	}
	if _, err := fixtureReply(model.Request{Role: domain.QA, Prompt: `{"extra":{}}`}); err == nil {
		t.Fatal("unbound review accepted")
	}
	index := 0
	c := domain.CodingReviewContract{MissionID: "owned", BindingDigest: strings.Repeat("a", 64), Required: []domain.CodingReviewReference{{ID: "acceptance:0", VerificationID: "verified", AcceptanceIndex: &index}}}
	raw, _ := json.Marshal(map[string]any{"extra": c})
	out, err := fixtureReply(model.Request{Role: domain.QA, Prompt: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"verification_id":"verified"`) || !strings.Contains(out, c.BindingDigest) {
		t.Fatal("review lost binding", out)
	}
}

func TestFixtureCredentialLeaseRejectsNonLocalSelection(t *testing.T) {
	l := &fixtureLease{base: "http://127.0.0.1:1234/v1"}
	if _, err := l.ResolveModelCredential(context.Background(), domain.RoleModelSelection{}); err == nil || l.reads.Load() != 0 {
		t.Fatal("unbound credential lease")
	}
}

func TestLocalFixtureAdmitsExactWireOnceAndRefusesRoleReorder(t *testing.T) {
	f := newLocalFixture()
	q := model.Request{InvocationID: "claimed-1", Role: domain.Researcher, System: "sealed-system", Prompt: `{"goal":"owned"}`, Profile: domain.Profile{Model: fixtureModel}, MaxOutputTokens: 8192}
	if err := f.authorize(q); err != nil {
		t.Fatal(err)
	}
	if err := f.authorize(q); err != nil {
		t.Fatal("repeated admission of same sealed claim", err)
	}
	body := `{"model":"SYNTHETIC-LOCAL-PROTOCOL","instructions":"sealed-system","input":[{"role":"user","content":"{\"goal\":\"owned\"}"}],"max_output_tokens":8192,"store":false}`
	send := func() int {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/responses", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+fixtureCredential)
		w := httptest.NewRecorder()
		f.ServeHTTP(w, r)
		return w.Code
	}
	if code := send(); code != 200 || len(f.calls) != 1 {
		t.Fatal("exact sealed wire rejected", code)
	}
	if err := f.authorize(q); err == nil {
		t.Fatal("consumed role admitted again")
	}
	if code := send(); code != 400 || len(f.calls) != 1 {
		t.Fatal("duplicate wire consumed fixture")
	}
}

func TestFixtureCredentialLeaseOnlyResolvesSyntheticLocalRoute(t *testing.T) {
	base := "http://127.0.0.1:1234/v1"
	l := &fixtureLease{base: base}
	s := domain.RoleModelSelection{Profile: domain.Profile{AccountID: fixtureAccount, Model: fixtureModel, BaseURL: base}, Account: &domain.ModelAccount{ID: fixtureAccount, BaseURL: base}, Route: &domain.ModelRoute{BaseURL: base, Environment: "simulation", Adapter: "openai_api_responses"}}
	lease, e := l.ResolveModelCredential(context.Background(), s)
	if e != nil || lease.Secret != fixtureCredential || lease.Generation != 1 || l.reads.Load() != 1 {
		t.Fatal("synthetic lease rejected", e)
	}
	s.Route.Environment = "production"
	if _, e = l.ResolveModelCredential(context.Background(), s); e == nil || l.reads.Load() != 1 {
		t.Fatal("production route admitted")
	}
}
