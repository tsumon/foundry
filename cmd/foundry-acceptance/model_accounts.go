package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/engine"
)

// Only the standalone synthetic acceptance binary has this implementation.
// Production model.Client retains its unconditional account inference denial.
func (syntheticGateway) PreflightAccountProfile(context.Context, domain.Profile) error { return nil }

// Values never leave process memory; no Keychain, database secret, or log output.
type ephemeralSecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *ephemeralSecrets) Get(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.values[id]
	if !ok {
		return "", errors.New("synthetic credential missing")
	}
	return value, nil
}
func (s *ephemeralSecrets) Set(id, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[id] = value
	return nil
}
func (s *ephemeralSecrets) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
	return nil
}

func seedModelAccounts(r *acceptanceRuntime, service *accounts.Service) error {
	ctx := context.Background()
	account, err := service.Save(domain.ModelAccount{Name: "SYNTHETIC R5A account", Provider: "custom", BaseURL: "http://127.0.0.1:9", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		return err
	}
	if _, err = service.AddCredential(ctx, account.ID, account.Revision, "synthetic-seed-v1", "SYNTHETIC-ONLY-NOT-A-PROVIDER-KEY-V1"); err != nil {
		return err
	}
	profile := domain.Profile{ID: "synthetic-account-profile", Name: "SYNTHETIC account model (inference closed)", Kind: "compatible", Protocol: "chat_completions", BaseURL: account.BaseURL, Model: "SYNTHETIC-STUB-NOT-CLOUD", AccountID: account.ID}
	if err = r.store.PutProfile(profile); err != nil {
		return err
	}
	mission, err := r.engine.Create(ctx, engine.CreateRequest{Title: "SYNTHETIC R5A frozen V1 approval", Goal: "Inspect historical account snapshot only; real account inference remains closed", RepoPath: r.Repo, WorkspaceMode: domain.WorkspaceModeGenerationsV1, Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "synthetic:stub", ProfileID: profile.ID, MaxCalls: 16}})
	if err != nil {
		return err
	}
	if _, err = r.engine.Plan(ctx, mission.ID, mission.Revision); err != nil {
		return err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		mission, err = r.store.GetMission(mission.ID)
		if err != nil {
			return err
		}
		if mission.State == "awaiting_approval" {
			break
		}
		if mission.State == "blocked" || time.Now().After(deadline) {
			return errors.New("synthetic account fixture planning failed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	account, err = r.store.GetModelAccount(account.ID)
	if err != nil {
		return err
	}
	if _, err = service.AddCredential(ctx, account.ID, account.Revision, "synthetic-seed-v2", "SYNTHETIC-ONLY-NOT-A-PROVIDER-KEY-V2"); err != nil {
		return err
	}
	r.AccountID, r.AccountMissionID = account.ID, mission.ID
	return nil
}
