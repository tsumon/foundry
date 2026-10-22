// Package testutil holds fixture-only adapters, never production authority.
package testutil

import (
	"foundry/internal/gitexec"
	"os/exec"
	"strings"
	"testing"
)

func Git(t testing.TB, args ...string) *exec.Cmd {
	t.Helper()
	c, e := gitexec.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(c.Close)
	argv := append([]string{"-c", "core.hooksPath=" + c.Hooks, "-c", "core.autocrlf=false", "-c", "commit.gpgsign=false"}, args...)
	for i, v := range argv {
		if strings.HasPrefix(v, "core.hooksPath=/dev/null") {
			argv[i] = "core.hooksPath=" + c.Hooks
		}
	}
	cmd := exec.Command(c.Path, argv...)
	cmd.Env = append(c.Environment, "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	// Fixture-only local clones are used to check exported patch applicability.
	// Never permit a remote protocol, and never relax the production config.
	for i, v := range cmd.Env {
		if v == "GIT_ALLOW_PROTOCOL=" {
			cmd.Env[i] = "GIT_ALLOW_PROTOCOL=file"
		}
	}
	return cmd
}
