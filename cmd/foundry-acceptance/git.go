package main

import (
	"foundry/internal/gitexec"
	"os/exec"
	"strings"
)

// Only disposable fixture creation uses this helper; imported sources remain
// read-only and all config/identity still comes from explicit trusted values.
func fixtureGitCommand(args ...string) (*exec.Cmd, func(), error) {
	c, e := gitexec.New()
	if e != nil {
		return nil, nil, e
	}
	argv := append([]string{"-c", "core.hooksPath=" + c.Hooks, "-c", "core.autocrlf=false"}, args...)
	for i, v := range argv {
		if strings.HasPrefix(v, "core.hooksPath=/dev/null") {
			argv[i] = "core.hooksPath=" + c.Hooks
		}
	}
	cmd := exec.Command(c.Path, argv...)
	cmd.Env = c.Environment
	return cmd, c.Close, nil
}
