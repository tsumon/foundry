//go:build (!darwin && !windows) || (darwin && !cgo)

package model

import (
	"errors"
	"testing"
)

func TestNativeSecretProbeStoreUnavailableWithoutFallback(t *testing.T) {
	store, err := NewSecretProbeStore("foundry-openai-stage0-0123456789abcdef0123456789abcdef")
	if store != nil || !errors.Is(err, ErrSecretStoreUnavailable) {
		t.Fatalf("store=%#v err=%v", store, err)
	}
}
