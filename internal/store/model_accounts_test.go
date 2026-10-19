package store

import (
	"errors"
	"testing"

	"foundry/internal/domain"
)

func TestModelAccountStoreCASAndCredentialTransitions(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic", Name: "Synthetic", Provider: "custom", BaseURL: "https://example.invalid", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "one", SecretRef: "foundry-model-account-synthetic-one"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || v.State != "pending" {
		t.Fatalf("intent: %+v", v)
	}
	if _, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "two", SecretRef: "foundry-model-account-synthetic-two"}, 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel intent: %v", err)
	}
	if _, err := s.FinishModelCredential("one", "pending"); err == nil {
		t.Fatal("pending reset accepted")
	}
	if _, err := s.FinishModelCredential("one", "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishModelCredential("one", "available"); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal revival: %v", err)
	}
	replay, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "one", SecretRef: "foundry-model-account-synthetic-replacement"}, 1)
	if err != nil || replay.Version != 1 || replay.State != "failed" || replay.SecretRef != v.SecretRef {
		t.Fatalf("terminal replay: %+v %v", replay, err)
	}
	two, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "two", SecretRef: "foundry-model-account-synthetic-two"}, 3)
	if err != nil || two.Version != 2 {
		t.Fatalf("next reservation: %+v %v", two, err)
	}
	if _, err := s.FinishModelCredential("two", "available"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetModelAccount(a.ID)
	if a.Revision != 5 || a.CurrentCredentialVersion != 2 {
		t.Fatalf("wrong current: %+v", a)
	}
	if err := s.CompleteModelCredentialCleanup(a.ID, 2); err == nil {
		t.Fatal("available secret marked cleaned")
	}
	if _, err := s.RevokeModelCredential(a.ID, 2, 4); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revoke: %v", err)
	}
	if _, err := s.RevokeModelCredential(a.ID, 99, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("future version: %v", err)
	}
	accounts, err := s.ListModelAccounts()
	if err != nil || len(accounts) != 1 {
		t.Fatalf("list: %+v %v", accounts, err)
	}
	cleanup, err := s.PendingModelCredentialCleanup()
	if err != nil || len(cleanup) != 1 || cleanup[0].Version != 1 {
		t.Fatalf("cleanup: %+v %v", cleanup, err)
	}
}

func TestCredentialLedgerRejectsDirectHistoryCorruption(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic", Name: "Synthetic", Provider: "openai", BaseURL: "https://example.invalid", AuthMethod: "api_key", BillingMode: "unknown"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "one", SecretRef: "foundry-model-account-synthetic-one"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishModelCredential("one", "available"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE model_credential_versions SET state='pending' WHERE operation_id='one'`,
		`UPDATE model_credential_versions SET secret_ref='replacement' WHERE operation_id='one'`,
		`DELETE FROM model_credential_versions WHERE operation_id='one'`,
		`UPDATE model_accounts SET provider='kimi' WHERE id='synthetic'`,
		`UPDATE model_accounts SET current_credential_version=99 WHERE id='synthetic'`,
		`UPDATE model_credential_versions SET state='revoked',cleanup_pending=1 WHERE operation_id='one'`,
		`INSERT INTO model_accounts (id,revision,name,provider,base_url,auth_method,billing_mode,enabled,current_credential_version,created_at,updated_at) VALUES('corrupt',1,'Synthetic','openai','https://example.invalid','api_key','unknown',1,99,'now','now')`,
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("ledger corruption accepted: %s", query)
		}
	}
}

func TestPendingCredentialSQLCannotLoseCleanupOrMutateAccount(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "synthetic", Name: "Synthetic", Provider: "openai", BaseURL: "https://example.invalid", AuthMethod: "api_key", BillingMode: "unknown"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "pending", SecretRef: "foundry-model-account-synthetic-pending"}, 1); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE model_credential_versions SET state='failed',cleanup_pending=0 WHERE operation_id='pending'`,
		`UPDATE model_accounts SET name='Mutated' WHERE id='synthetic'`,
		`UPDATE model_accounts SET enabled=1 WHERE id='synthetic'`,
	} {
		if _, err := s.db.Exec(query); err == nil {
			t.Fatalf("unsafe pending mutation accepted: %s", query)
		}
	}
}
