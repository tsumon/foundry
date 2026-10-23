package model

import (
	"errors"
	"strings"
	"testing"
)

type fakeCredentialManager struct {
	values       map[string][]byte
	setTarget    string
	getTarget    string
	deleteTarget string
	err          error
}

func (f *fakeCredentialManager) set(target string, value []byte) error {
	f.setTarget = target
	if f.err != nil {
		return f.err
	}
	if f.values == nil {
		f.values = map[string][]byte{}
	}
	f.values[target] = append([]byte(nil), value...)
	return nil
}

func (f *fakeCredentialManager) get(target string) ([]byte, error) {
	f.getTarget = target
	if f.err != nil {
		return nil, f.err
	}
	value, ok := f.values[target]
	if !ok {
		return nil, errCredentialManagerNotFound
	}
	return append([]byte(nil), value...), nil
}

func (f *fakeCredentialManager) delete(target string) error {
	f.deleteTarget = target
	if f.err != nil {
		return f.err
	}
	delete(f.values, target)
	return nil
}

func TestCredentialManagerSecretsRoundTripUsesFoundryScopedOpaqueTarget(t *testing.T) {
	native := &fakeCredentialManager{}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	if err := store.Set("foundry-model-account-0123456789abcdef", "private-value"); err != nil {
		t.Fatal(err)
	}
	if native.setTarget == "" || !strings.HasPrefix(native.setTarget, credentialManagerSecretNamespace) || strings.Contains(native.setTarget, "private-value") {
		t.Fatalf("unsafe target %q", native.setTarget)
	}
	got, err := store.Get("foundry-model-account-0123456789abcdef")
	if err != nil || got != "private-value" || native.getTarget != native.setTarget {
		t.Fatalf("got=%q err=%v targets=%q/%q", got, err, native.setTarget, native.getTarget)
	}
	if err = store.Delete("foundry-model-account-0123456789abcdef"); err != nil || native.deleteTarget != native.setTarget {
		t.Fatalf("delete err=%v target=%q", err, native.deleteTarget)
	}
	if _, err = store.Get("foundry-model-account-0123456789abcdef"); !errors.Is(err, ErrSecretNotFound) {
		t.Fatalf("missing err=%v", err)
	}
}

func TestCredentialManagerProbeStoreIsExactAndNamespaceIsolated(t *testing.T) {
	id := "foundry-openai-stage0-0123456789abcdef0123456789abcdef"
	native := &fakeCredentialManager{}
	store := newCredentialManagerSecrets(native, credentialManagerProbeNamespace, id)
	if err := store.Set(id, "synthetic"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(native.setTarget, credentialManagerProbeNamespace) || strings.HasPrefix(native.setTarget, credentialManagerSecretNamespace) {
		t.Fatalf("probe target not isolated: %q", native.setTarget)
	}
	for _, other := range []string{"account-id", id + "0"} {
		if err := store.Set(other, "synthetic"); err == nil {
			t.Fatalf("probe accepted %q", other)
		}
	}
}

func TestCredentialManagerSecretsValidateIdentifiersAndBlobSizeBeforeNativeCall(t *testing.T) {
	for _, id := range []string{"", "   ", strings.Repeat("a", 257), "line\nbreak", "nul\x00byte"} {
		native := &fakeCredentialManager{}
		store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
		if err := store.Set(id, "value"); err == nil || native.setTarget != "" {
			t.Fatalf("id %q err=%v target=%q", id, err, native.setTarget)
		}
	}
	native := &fakeCredentialManager{}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	for _, value := range []string{"", strings.Repeat("x", credentialManagerMaxBlobSize+1)} {
		if err := store.Set("safe-id", value); err == nil || native.setTarget != "" {
			t.Fatalf("value length=%d err=%v target=%q", len(value), err, native.setTarget)
		}
	}
}

func TestCredentialManagerBlobLimitCountsExactUTF8Bytes(t *testing.T) {
	native := &fakeCredentialManager{}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	boundary := strings.Repeat("密", 853) + "x"
	if len(boundary) != credentialManagerMaxBlobSize {
		t.Fatalf("fixture bytes=%d", len(boundary))
	}
	if err := store.Set("safe-id", boundary); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("safe-id")
	if err != nil || got != boundary {
		t.Fatalf("multibyte round trip bytes=%d err=%v", len(got), err)
	}
	if err = store.Set("safe-id", boundary+"y"); err == nil {
		t.Fatal("native byte boundary plus one was accepted")
	}
}

func TestCredentialManagerErrorsAreSanitized(t *testing.T) {
	native := &fakeCredentialManager{err: errors.New("native failure includes PRIVATE-VALUE and target details")}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{"set", func() error { return store.Set("safe-id", "PRIVATE-VALUE") }},
		{"get", func() error { _, err := store.Get("safe-id"); return err }},
		{"delete", func() error { return store.Delete("safe-id") }},
	} {
		err := operation.run()
		if err == nil || strings.Contains(err.Error(), "PRIVATE-VALUE") || strings.Contains(err.Error(), "native failure") || strings.Contains(err.Error(), "safe-id") {
			t.Fatalf("%s leaked native detail: %v", operation.name, err)
		}
	}
}

func TestCredentialManagerRejectsInvalidNativeBlobWithoutPublishingIt(t *testing.T) {
	for _, value := range [][]byte{nil, []byte(strings.Repeat("x", credentialManagerMaxBlobSize+1))} {
		native := &fakeCredentialManager{values: map[string][]byte{}}
		store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
		target, err := store.target("safe-id")
		if err != nil {
			t.Fatal(err)
		}
		native.values[target] = value
		got, err := store.Get("safe-id")
		if err == nil || got != "" || strings.Contains(err.Error(), strings.Repeat("x", 32)) {
			t.Fatalf("length=%d got=%q err=%v", len(value), got, err)
		}
	}
}
