//go:build windows

package model

import (
	"errors"
	"os"
	"testing"
)

func TestWindowsSecretConstructorsDoNotAccessNativeStore(t *testing.T) {
	store, ok := NewSecrets().(*credentialManagerSecrets)
	if !ok || store.namespace != credentialManagerSecretNamespace || store.probeID != "" {
		t.Fatalf("unexpected primary store: %#v", store)
	}
	id := "foundry-openai-stage0-0123456789abcdef0123456789abcdef"
	probe, err := NewSecretProbeStore(id)
	if err != nil {
		t.Fatal(err)
	}
	nativeProbe, ok := probe.(*credentialManagerSecrets)
	if !ok || nativeProbe.namespace != credentialManagerProbeNamespace || nativeProbe.probeID != id || nativeProbe.namespace == store.namespace {
		t.Fatalf("probe store is not isolated: %#v", probe)
	}
	if _, err = NewSecretProbeStore("account-id"); err == nil {
		t.Fatal("arbitrary diagnostic identifier accepted")
	}
}

func TestWindowsCredentialManagerNativeCIProbe(t *testing.T) {
	if os.Getenv("FOUNDRY_WINDOWS_CREDENTIAL_MANAGER_NATIVE_CI") != "1" {
		t.Skip("requires the exact native Windows CI credential-manager opt-in")
	}
	id, err := GenerateSecretProbeID()
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewSecretProbeStore(id)
	if err != nil {
		t.Fatal(err)
	}
	nativeStore, ok := store.(*credentialManagerSecrets)
	if !ok || nativeStore.namespace != credentialManagerProbeNamespace || nativeStore.namespace == credentialManagerSecretNamespace || nativeStore.probeID != id {
		t.Fatal("native CI probe escaped the isolated diagnostic namespace")
	}
	defer func() {
		if err := store.Delete(id); err != nil {
			t.Errorf("diagnostic cleanup failed: %v", err)
		}
	}()
	const value = "synthetic-foundry-windows-credential-manager-ci"
	if err = store.Set(id, value); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(id)
	if err != nil || got != value {
		t.Fatalf("diagnostic round trip mismatch: value=%t err=%v", got == value, err)
	}
	if err = store.Delete(id); err != nil {
		t.Fatal(err)
	}
	if got, err = store.Get(id); got != "" || !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("diagnostic deletion not confirmed: value_empty=%t missing=%t", got == "", errors.Is(err, ErrSecretNotFound))
	}
}
