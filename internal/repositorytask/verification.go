package repositorytask

import (
	"foundry/internal/c4verify"
	"reflect"
)

// Evaluate is pure: assertions come exclusively from the frozen descriptor.
// Counts are simulated content checks, never Go test execution counts.
func Evaluate(d Definition, frozen, candidate map[string]string, kind string) c4verify.Result {
	bad := c4verify.Result{Class: "infrastructure_failed", ExitCode: 2, Output: "SYNTHETIC content check rejected invalid frozen input; no commands executed"}
	parsed, err := Parse([]byte(frozen[Descriptor]))
	if err != nil || !reflect.DeepEqual(parsed, d) || (kind != "baseline" && kind != "candidate") {
		return bad
	}
	editable := map[string]bool{}
	for _, s := range d.Steps {
		for _, e := range s.Edits {
			editable[e.Path] = true
		}
	}
	for p, body := range frozen {
		actual, ok := candidate[p]
		if !ok || ((kind == "baseline" || !editable[p]) && actual != body) {
			return bad
		}
	}
	for p := range candidate {
		if _, ok := frozen[p]; !ok && (kind == "baseline" || !editable[p]) {
			return bad
		}
	}
	result := c4verify.Result{Class: "passed", Tests: len(d.Checks), Output: "SYNTHETIC exact-content comparison only; no compiler, tests, Docker or process ran"}
	if kind == "candidate" {
		for _, check := range d.Checks {
			actual, ok := candidate[check.Path]
			if !ok {
				return bad
			}
			if actual != *check.Content {
				result.Class = "test_failed"
				result.ExitCode = 1
			}
		}
	}
	return result
}
