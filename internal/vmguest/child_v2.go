package vmguest

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/platformfs"
	"slices"
	"strings"
)

type phaseV2 string

const (
	probeNodeV2         phaseV2 = "probe-node"
	probePythonV2       phaseV2 = "probe-python"
	probeTypeScriptV2   phaseV2 = "probe-typescript"
	compileTypeScriptV2 phaseV2 = "compile-typescript"
	testNodeV2          phaseV2 = "test-node"
	testPythonV2        phaseV2 = "test-python"
)

var errGuestV2 = errors.New("guest v2 boundary rejected")
var envV2 = []string{"LANG=C.UTF-8", "TZ=UTC", "TMPDIR=/tmp"}

type childContractV2 struct {
	SchemaVersion int      `json:"schema_version"`
	Phase         phaseV2  `json:"phase"`
	SourceRoots   []string `json:"source_roots"`
	TestFiles     []string `json:"test_files"`
	InputFiles    []string `json:"input_files"`
	SourceRoot    string   `json:"source_root"`
	OutputRoot    string   `json:"output_root"`
}

func (p phaseV2) valid() bool {
	switch p {
	case probeNodeV2, probePythonV2, probeTypeScriptV2, compileTypeScriptV2, testNodeV2, testPythonV2:
		return true
	}
	return false
}
func (p phaseV2) test() bool { return p == testNodeV2 || p == testPythonV2 }
func (p phaseV2) probe() bool {
	return p == probeNodeV2 || p == probePythonV2 || p == probeTypeScriptV2
}
func relativeV2(p string) bool {
	if len(p) == 0 || len(p) > 1024 || platformfs.WindowsRelative(p) != nil || strings.Count(p, "/") >= 32 {
		return false
	}
	for _, b := range []byte(p) {
		if b < 32 || b > 126 || b == '\\' {
			return false
		}
	}
	return true
}
func contractListV2(a []string) bool {
	if a == nil || len(a) > 500 {
		return false
	}
	for i, p := range a {
		if !relativeV2(p) || (i > 0 && p <= a[i-1]) {
			return false
		}
	}
	return true
}
func (c childContractV2) valid(p phaseV2) bool {
	if c.SchemaVersion != 1 || !p.valid() || c.Phase != p || c.SourceRoot != "/workspace" || c.SourceRoots == nil || c.TestFiles == nil || c.InputFiles == nil || !contractListV2(c.TestFiles) || !contractListV2(c.InputFiles) {
		return false
	}
	if p.probe() {
		return len(c.SourceRoots) == 0 && len(c.TestFiles) == 0 && len(c.InputFiles) == 0 && c.OutputRoot == ""
	}
	if len(c.SourceRoots) != 1 || (c.SourceRoots[0] != "." && c.SourceRoots[0] != "src") {
		return false
	}
	switch p {
	case compileTypeScriptV2:
		if c.OutputRoot != "/foundry-build/compiled" || len(c.InputFiles) == 0 || len(c.TestFiles) != 0 {
			return false
		}
		for _, f := range c.InputFiles {
			if !strings.HasSuffix(f, ".ts") {
				return false
			}
		}
	case testNodeV2, testPythonV2:
		if len(c.TestFiles) == 0 || len(c.InputFiles) != 0 || (c.OutputRoot != "" && (p != testNodeV2 || c.OutputRoot != "/foundry-build/compiled")) {
			return false
		}
	}
	return true
}

// Canonical byte equality makes the internal FD contract exact-key, duplicate
// free, non-null and bounded without a second permissive JSON grammar.
func parseChildContractV2(b []byte, p phaseV2) (childContractV2, error) {
	var c childContractV2
	if len(b) == 0 || len(b) > 1<<20 || json.Unmarshal(b, &c) != nil || !c.valid(p) {
		return childContractV2{}, errGuestV2
	}
	raw, e := json.Marshal(c)
	if e != nil || !bytes.Equal(b, raw) {
		return childContractV2{}, errGuestV2
	}
	return c, nil
}
func childEnvironmentV2(env []string) bool { return slices.Equal(env, envV2) }
func executionV2(p phaseV2) (string, []string, error) {
	switch p {
	case testNodeV2, probeNodeV2:
		return "/opt/foundry/node/bin/node", []string{"/opt/foundry/runners/node.cjs"}, nil
	case testPythonV2, probePythonV2:
		return "/usr/bin/python3.13", []string{"-I", "-S", "-B", "/opt/foundry/runners/python.py"}, nil
	case compileTypeScriptV2, probeTypeScriptV2:
		return "/opt/foundry/node/bin/node", []string{"/opt/foundry/runners/typescript.cjs"}, nil
	}
	return "", nil, errGuestV2
}

type childObservationV2 struct {
	uid, euid, gid, egid, ppid, nnp                                                                     int
	groups                                                                                              []int
	env                                                                                                 []string
	cwd                                                                                                 string
	stdinNull, stdoutPipe, stderrPipe, reportPipe, contractReadonly, contractSealed, privateDescriptors bool
}

func validateChildObservationV2(p phaseV2, o childObservationV2) error {
	if !p.valid() || o.uid != 65534 || o.euid != 65534 || o.gid != 65534 || o.egid != 65534 || o.ppid != 1 || o.nnp != 1 || len(o.groups) != 0 || !childEnvironmentV2(o.env) || o.cwd != "/workspace" || !o.stdinNull || !o.stdoutPipe || !o.stderrPipe || o.reportPipe != p.test() || !o.contractReadonly || !o.contractSealed || !o.privateDescriptors {
		return errGuestV2
	}
	return nil
}
