package engine

import (
	"context"
	"foundry/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

type contentFixtureRunner struct{}

func (contentFixtureRunner) Resolve(context.Context, string) (string, error) {
	return "sha256:fixture", nil
}
func (contentFixtureRunner) Reconcile(context.Context) error { return nil }
func (contentFixtureRunner) Run(_ context.Context, _, candidate, _ string, command string) (domain.CommandResult, error) {
	b, err := os.ReadFile(filepath.Join(candidate, "src/main.txt"))
	result := domain.CommandResult{Command: command, ExitCode: 1, Output: "SIMULATED content check; no command executed"}
	if err == nil && string(b) == "repaired\n" && command == "test -f /workspace/src/main.txt" {
		result.ExitCode = 0
	}
	return result, err
}

func TestCandidateContentDeterminesMockVerification(t *testing.T) {
	e, _, _, _, m := setup(t)
	candidate, err := e.selectedCandidate(m)
	if err != nil {
		t.Fatal(err)
	}
	checker := contentFixtureRunner{}
	command := "test -f /workspace/src/main.txt"
	for i := 0; i < 2; i++ {
		result, err := checker.Run(context.Background(), m.ID, candidate, "sha256:fixture", command)
		if err != nil || result.ExitCode == 0 {
			t.Fatal("unchanged wrong candidate became passing")
		}
	}
	if err := os.WriteFile(filepath.Join(candidate, "src/main.txt"), []byte("repaired\n"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := checker.Run(context.Background(), m.ID, candidate, "sha256:fixture", command)
	if err != nil || result.ExitCode != 0 {
		t.Fatal("correct content did not pass")
	}
}
