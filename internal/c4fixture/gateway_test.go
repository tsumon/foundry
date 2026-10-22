package c4fixture

import (
	"context"
	"foundry/internal/model"
	"foundry/internal/store"
	"path/filepath"
	"testing"
)

func TestUnsupportedGoalHasNoFallback(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = New(s).Complete(context.Background(), model.Request{Prompt: `{"goal":"unsupported","coding_identity":{"mission_id":"missing"}}`}); err == nil {
		t.Fatal("unsupported goal accepted")
	}
}
