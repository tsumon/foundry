package main

import (
	"context"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCodingDesktopOptionsAreExplicitAndExclusive(t *testing.T) {
	if yes, err := desktopOptions([]string{"--desktop", "--coding-workbench"}); err != nil || !yes {
		t.Fatal(yes, err)
	}
	for _, args := range [][]string{{"--coding-workbench"}, {"--desktop", "--coding-workbench=true"}, {"--desktop", "--coding-workbench", "--local-mcp-test"}} {
		if _, err := desktopOptions(args); err == nil {
			t.Fatal("accepted", args)
		}
	}
}

type codingForbiddenSecrets struct{ calls int }

func (s *codingForbiddenSecrets) Get(string) (string, error) {
	s.calls++
	return "", model.ErrSecretStoreUnavailable
}
func (s *codingForbiddenSecrets) Set(string, string) error {
	s.calls++
	return model.ErrSecretStoreUnavailable
}
func (s *codingForbiddenSecrets) Delete(string) error {
	s.calls++
	return model.ErrSecretStoreUnavailable
}

func TestCodingStartupAndRecoveryDoNotTouchNativeCredentials(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "coding.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "pending", Name: "Pending", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: "https://api.openai.com/v1", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "unfinished", SecretRef: "foundry-model-account-pending"}, a.Revision); err != nil {
		t.Fatal(err)
	}
	secrets := &codingForbiddenSecrets{}
	models, e, err := newCodingRuntime(s, t.TempDir(), secrets)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	defer models.siwc.Close()
	defer models.acceptance.Close()
	if err = models.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil && runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" && e.CodingWorkbenchAvailable() {
		t.Fatal("Windows Docker trust unavailable")
	}
	if secrets.calls != 0 {
		t.Fatal("startup accessed native credentials", secrets.calls)
	}
}
