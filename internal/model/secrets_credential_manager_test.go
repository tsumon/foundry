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
	setCalls     int
	failSet      int
	failDelete   string
	keepDeleted  bool
	beforeSet    func(string, []byte)
}

func (f *fakeCredentialManager) set(target string, value []byte) error {
	f.setCalls++
	if f.beforeSet != nil {
		f.beforeSet(target, value)
	}
	if len(value) > 2560 {
		return errors.New("native limit")
	}
	if f.setCalls == f.failSet {
		return errors.New("interrupted")
	}
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
	if target == f.failDelete {
		return errors.New("delete denied")
	}
	if f.keepDeleted {
		return nil
	}
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
	if err = store.Delete("foundry-model-account-0123456789abcdef"); err != nil || len(native.values) != 0 {
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
	for _, value := range []string{"", strings.Repeat("x", 256*1024+1)} {
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
	if err = store.Set("safe-id", boundary+"y"); err != nil {
		t.Fatal("segmented byte boundary plus one was rejected")
	}
}

func TestCredentialManagerSegmentedRoundTripAndBoundedCleanup(t *testing.T) {
	for _, size := range []int{2561, 12000, 256 * 1024} {
		native := &fakeCredentialManager{}
		store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
		value := strings.Repeat("abc", size/3) + strings.Repeat("x", size%3)
		if err := store.Set("subscription", value); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		got, err := store.Get("subscription")
		if err != nil || got != value {
			t.Fatalf("size %d roundtrip: %v", size, err)
		}
		if len(native.values) < 3 || len(native.values) > 104 {
			t.Fatalf("unbounded representation: %d", len(native.values))
		}
		for _, blob := range native.values {
			if len(blob) > 2560 {
				t.Fatal("native limit exceeded")
			}
		}
		if err := store.Set("neighboring-reference", "untouched"); err != nil {
			t.Fatal(err)
		}
		neighbor, _ := store.target("neighboring-reference")
		root, _ := store.target("subscription")
		delete(native.values, root) // Process died before publication, root absent.
		if err = store.Delete("subscription"); err != nil {
			t.Fatal(err)
		}
		if len(native.values) != 1 || string(native.values[neighbor]) != "untouched" {
			t.Fatal("orphan cleanup incomplete or unrelated item touched")
		}
	}
}

func TestCredentialManagerInterruptedWritesNeverPublish(t *testing.T) {
	for _, fail := range []int{1, 2, 3, 4} { // pending, two chunks, commit
		native := &fakeCredentialManager{failSet: fail}
		store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
		if err := store.Set("interrupted", strings.Repeat("x", 3000)); err == nil {
			t.Fatalf("failure %d ignored", fail)
		}
		if native.setCalls != fail {
			t.Fatal("injected native write failure was not exercised")
		}
		if got, err := store.Get("interrupted"); err == nil || got != "" {
			t.Fatal("failed write readable")
		}
		if err := store.Delete("interrupted"); err != nil || len(native.values) != 0 {
			t.Fatal("interrupted cleanup incomplete", err)
		}
	}
}

func TestCredentialManagerPendingAndCorruptSegmentsFailClosed(t *testing.T) {
	for _, damage := range []string{"pending", "missing", "tampered", "reordered", "malformed", "truncated-marker", "oversized"} {
		t.Run(damage, func(t *testing.T) {
			native := &fakeCredentialManager{}
			store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
			root, _ := store.target("corrupt")
			var pending []byte
			native.beforeSet = func(target string, blob []byte) {
				if target == root && pending == nil {
					pending = append([]byte(nil), blob...)
				}
			}
			if err := store.Set("corrupt", strings.Repeat("a", 2560)+strings.Repeat("b", 2560)); err != nil {
				t.Fatal(err)
			}
			var chunks []string
			for target := range native.values {
				if target != root {
					chunks = append(chunks, target)
				}
			}
			switch damage {
			case "pending":
				native.values[root] = pending
			case "missing":
				delete(native.values, chunks[0])
			case "tampered":
				native.values[chunks[0]][0] ^= 1
			case "reordered":
				native.values[chunks[0]], native.values[chunks[1]] = native.values[chunks[1]], native.values[chunks[0]]
			case "malformed":
				native.values[root] = native.values[root][:len(native.values[root])-1]
			case "truncated-marker":
				native.values[root] = native.values[root][:1]
			case "oversized":
				native.values[chunks[0]] = make([]byte, 2561)
			}
			if got, err := store.Get("corrupt"); err == nil || got != "" {
				t.Fatal("corrupt credential readable")
			}
		})
	}
}

func TestCredentialManagerDeletionFailureRemainsUnconfirmed(t *testing.T) {
	native := &fakeCredentialManager{}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	if err := store.Set("delete", strings.Repeat("x", 3000)); err != nil {
		t.Fatal(err)
	}
	root, _ := store.target("delete")
	for target := range native.values {
		if target != root {
			native.failDelete = target
			break
		}
	}
	delete(native.values, root)
	if err := store.Delete("delete"); err == nil {
		t.Fatal("orphan delete failure ignored")
	}
	native.failDelete = ""
	native.keepDeleted = true
	if err := store.Delete("delete"); err == nil {
		t.Fatal("absence not verified")
	}
	native.keepDeleted = false
	if err := store.Delete("delete"); err != nil || len(native.values) != 0 {
		t.Fatal("cleanup retry failed", err)
	}
}

func TestCredentialManagerLegacyRawSecretStillReadable(t *testing.T) {
	native := &fakeCredentialManager{values: map[string][]byte{}}
	store := newCredentialManagerSecrets(native, credentialManagerSecretNamespace, "")
	root, _ := store.target("legacy")
	native.values[root] = []byte("legacy-small-value")
	got, err := store.Get("legacy")
	if err != nil || got != "legacy-small-value" {
		t.Fatal("legacy read failed", err)
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
