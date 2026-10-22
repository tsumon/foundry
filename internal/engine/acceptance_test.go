package engine

import (
	"context"
	"foundry/internal/testutil"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcceptanceRunnerFrozenBeforePlanAndEdit(t *testing.T) {
	e, s, _, _, original := setup(t, strings.ReplaceAll(plan, "src/main.txt", "scripts/check.sh"))
	repo := original.RepoPath
	if err := os.Mkdir(filepath.Join(repo, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "scripts/check.sh"), []byte("#!/bin/sh\ngrep expected /workspace/src/main.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "runner"}} {
		c := testutil.Git(t, args...)
		c.Dir = repo
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, b)
		}
	}
	spec := original.Spec
	spec.Scope = []string{"scripts/", "src/"}
	spec.Research = false
	spec.Acceptance = []string{"sh scripts/check.sh"}
	m, err := e.Create(context.Background(), CreateRequest{Title: "runner", Goal: "change", RepoPath: repo, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	op := e.active
	e.mu.Unlock()
	if op != nil {
		<-op.finished
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "blocked" || !strings.Contains(m.Error, "protected") {
		t.Errorf("acceptance-edit plan not rejected: %s %s", m.State, m.Error)
	}
	candidate := filepath.Join(e.dir(m.ID), "candidate")
	if _, err := workspace.Apply(candidate, []workspace.Edit{{Path: "scripts/check.sh", Content: "exit 0\n"}}, spec.Scope, m.Spec.Protected); err == nil {
		t.Error("runner replacement applied")
	}
	b, _ := os.ReadFile(filepath.Join(candidate, "scripts/check.sh"))
	if string(b) != "#!/bin/sh\ngrep expected /workspace/src/main.txt\n" {
		t.Error("runner weakened")
	}
	if _, err := workspace.Apply(candidate, []workspace.Edit{{Path: "src/main.txt", Content: "expected\n"}}, spec.Scope, m.Spec.Protected); err != nil {
		t.Fatalf("grep subject must remain editable: %v", err)
	}
}

func TestUnsupportedAcceptanceRejectedBeforePlanning(t *testing.T) {
	e, _, g, _, m := setup(t)
	for _, command := range []string{"npm test", "sh -c 'exit 0'", "grep expected src/main.txt; true", "grep $(cat src/main.txt) src/main.txt", "python scripts/check.py", "sh missing.sh", "/opt/foundry/../other", "env PATH=/workspace /opt/foundry/verify"} {
		t.Run(command, func(t *testing.T) {
			spec := m.Spec
			spec.Acceptance = []string{command}
			if _, err := e.Create(context.Background(), CreateRequest{Title: "command", Goal: "check", RepoPath: m.RepoPath, Spec: spec}); err == nil {
				t.Fatal("ambiguous acceptance accepted")
			}
		})
	}
	if len(g.roles) != 0 {
		t.Fatal("rejected grammar bought a model call")
	}
}
