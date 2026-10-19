//go:build darwin && cgo

package model

import "testing"

func TestNativeSecretProbeStoreUsesIsolatedFixedNamespace(t *testing.T) {
	id := "foundry-openai-stage0-0123456789abcdef0123456789abcdef"
	store, err := NewSecretProbeStore(id)
	if err != nil {
		t.Fatal(err)
	}
	native, ok := store.(*keychainSecrets)
	if !ok || native.service == keychainService || native.service != keychainProbeService || native.probeID != id {
		t.Fatalf("probe store is not isolated: %#v", store)
	}
	if _, err := NewSecretProbeStore("account-id"); err == nil {
		t.Fatal("arbitrary identifier accepted")
	}
}
