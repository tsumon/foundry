package acceptance

import (
	"context"
	"database/sql"
	"errors"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestProductionCannotProposeOrExecute(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	svc := New(s)
	if svc.SimulationEligible("forged") {
		t.Fatal("production eligible")
	}
	if _, e = svc.Propose("forged", domain.ModelAcceptanceProposal{Phase: "connection", RouteID: "loopback", ExpectedAccountRevision: 1}); e != ErrDisabled {
		t.Fatalf("propose %v", e)
	}
	if _, e = svc.Execute(context.Background(), "forged", "forged", 1, "forged"); e != ErrDisabled {
		t.Fatalf("execute %v", e)
	}
}

type gateSecrets map[string]string

func (s gateSecrets) Get(id string) (string, error) {
	v, ok := s[id]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}
func (s gateSecrets) Set(id, v string) error { s[id] = v; return nil }
func (s gateSecrets) Delete(id string) error { delete(s, id); return nil }

func TestServiceLostCompletionDisablesExecutionAndRecoveryConsumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gate.db")
	s, e := store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := s.SaveModelAccount(domain.ModelAccount{ID: "owned", Name: "owned", Provider: "kimi", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.CreateModelRoute(domain.ModelRoute{ID: "owned-route", AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"})
	if e != nil {
		t.Fatal(e)
	}
	service := accounts.New(s, gateSecrets{})
	calls := 0
	options := Options{Routes: []domain.ModelRoute{r}, Router: model.NewRouter(nil, service, model.RouterOptions{SimulationOrigins: []string{"http://127.0.0.1:12345"}}), Connect: func(ctx context.Context, m domain.ModelAcceptanceMaterial) error {
		calls++
		if _, e := service.AddCredential(ctx, a.ID, m.Account.Revision, "synthetic-key-write", "SYNTHETIC-SECRET-MARKER"); e != nil {
			return e
		}
		_, e := db.Exec(`CREATE TRIGGER fail_completion BEFORE UPDATE ON model_acceptance_operations WHEN NEW.state!='running' BEGIN SELECT RAISE(ABORT,'private-fault-text'); END`)
		return e
	}}
	svc := NewSimulation(s, options)
	if svc.SimulationEligible(a.ID) {
		t.Fatal("enabled before recovery")
	}
	if e = svc.Recover(); e != nil {
		t.Fatal(e)
	}
	o, e := svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision})
	if e != nil {
		t.Fatal(e)
	}
	o, e = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest); e != ErrPersistence {
		t.Fatalf("write failure: %v", e)
	}
	if calls != 1 || svc.SimulationEligible(a.ID) {
		t.Fatal("write failure did not disable")
	}
	got, _ := svc.Get(a.ID, o.ID)
	if got.State != "running" || got.Attempts != 1 {
		t.Fatalf("consumption not durable %+v", got)
	}
	if e = svc.Recover(); e != ErrPersistence {
		t.Fatal("recovery failure hidden")
	}
	if _, e = db.Exec(`DROP TRIGGER fail_completion`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	s, e = store.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	svc = NewSimulation(s, options)
	if e = svc.Recover(); e != nil {
		t.Fatal(e)
	}
	got, _ = svc.Get(a.ID, o.ID)
	if got.State != "unknown" || got.Attempts != 1 {
		t.Fatalf("restart %+v", got)
	}
	if _, e = svc.Execute(context.Background(), a.ID, o.ID, got.Revision, got.MaterialDigest); e == nil || calls != 1 {
		t.Fatal("restart replay")
	}
}
func TestServiceUnknownAndExpiryStayInert(t *testing.T) {
	s, e := store.Open(filepath.Join(t.TempDir(), "gate.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a, _ := s.SaveModelAccount(domain.ModelAccount{ID: "owned", Name: "owned", Provider: "kimi", BaseURL: "http://127.0.0.1:12345/v1", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	r, _ := s.CreateModelRoute(domain.ModelRoute{ID: "owned-route", AccountID: a.ID, Adapter: "kimi_code_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "interactive_package", ExtraUsage: "off"})
	now := time.Now()
	calls := 0
	svc := NewSimulation(s, Options{Routes: []domain.ModelRoute{r}, Router: model.NewRouter(nil, nil, model.RouterOptions{}), Now: func() time.Time { return now }, Connect: func(context.Context, domain.ModelAcceptanceMaterial) error {
		calls++
		return errors.New("raw-SYNTHETIC-SECRET-response")
	}})
	if e = svc.Recover(); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		o, e := svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision})
		if e != nil {
			t.Fatal(e)
		}
		o, e = svc.Decide(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve")
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 {
			now = now.Add(11 * time.Minute)
		}
		got, e := svc.Execute(context.Background(), a.ID, o.ID, o.Revision, o.MaterialDigest)
		if i == 0 && (e != nil || got.State != "unknown" || got.Code != "transport_unknown") {
			t.Fatalf("unknown %+v %v", got, e)
		}
		if i == 1 && e == nil {
			t.Fatal("expired dispatch")
		}
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Propose(a.ID, domain.ModelAcceptanceProposal{Phase: "connection", RouteID: r.ID, ExpectedAccountRevision: a.Revision}); e != ErrPersistence || svc.SimulationEligible(a.ID) {
		t.Fatalf("storage failure left simulation enabled: %v", e)
	}
}
