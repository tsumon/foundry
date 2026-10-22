package main

import (
	"errors"
	"foundry/internal/model"
	"testing"
)

func TestDesktopSecretsFailClosed(t *testing.T) {
	secrets := desktopSecrets{}
	if err := secrets.Set("id", "value"); !errors.Is(err, model.ErrSecretStoreUnavailable) {
		t.Fatal(err)
	}
	if _, err := secrets.Get("id"); !errors.Is(err, model.ErrSecretStoreUnavailable) {
		t.Fatal(err)
	}
	if err := secrets.Delete("id"); !errors.Is(err, model.ErrSecretStoreUnavailable) {
		t.Fatal(err)
	}
}

// Desktop must never inherit operator activation or permit a persistent port.
func TestDesktopStartupOptions(t *testing.T) {
	for _, args := range [][]string{{"-desktop", "--local-mcp-test"}, {"-desktop"}, {"--desktop", "--local-mcp-test"}, {"--desktop", "--c4-control-config=x"}, {"--desktop", "--openai-inference-route=x"}, {"--desktop=false"}} {
		if _, err := desktopOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if yes, err := desktopOptions([]string{"--desktop"}); err != nil || !yes {
		t.Fatal(yes, err)
	}
	t.Setenv("FOUNDRY_ADDR", "127.0.0.1:0")
	if _, err := loadConfig(); err == nil {
		t.Fatal("CLI accepted zero port")
	}
	if _, err := loadConfigMode(true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FOUNDRY_ADDR", "127.0.0.1:4317")
	if _, err := loadConfigMode(true); err == nil {
		t.Fatal("desktop accepted fixed port")
	}
}
