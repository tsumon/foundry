package repositorytask

import "testing"

func TestVerificationUsesFrozenAssertionsAndSeparateKinds(t *testing.T) {
	d, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	frozen := map[string]string{"foundry.task.json": example, "go.mod": "module labels\n", "go.sum": "", "internal/label/label.go": "original", "internal/label/display.go": "old"}
	copyFiles := func() map[string]string {
		out := map[string]string{}
		for p, c := range frozen {
			out[p] = c
		}
		return out
	}
	base := copyFiles()
	if got := Evaluate(d, frozen, base, "baseline"); got.Class != "passed" {
		t.Fatal(got)
	}
	if got := Evaluate(d, frozen, base, "candidate"); got.Class != "test_failed" {
		t.Fatal("baseline falsely satisfied candidate", got)
	}
	candidate := copyFiles()
	candidate["internal/label/label.go"] = "normalized"
	candidate["internal/label/display.go"] = "display"
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "passed" || got.Tests != 2 {
		t.Fatal(got)
	}
	candidate["foundry.task.json"] = "{}"
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "infrastructure_failed" {
		t.Fatal("mutable assertions", got)
	}
	for _, kind := range []string{"", "real", "baseline"} {
		if got := Evaluate(d, frozen, candidate, kind); got.Class == "passed" {
			t.Fatal(kind, got)
		}
	}
	candidate = copyFiles()
	delete(candidate, "go.mod")
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "infrastructure_failed" {
		t.Fatal("missing frozen file", got)
	}
	candidate = copyFiles()
	candidate["unapproved.go"] = "extra"
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "infrastructure_failed" {
		t.Fatal("extra file", got)
	}
}

func TestVerificationAllowsOnlyDeclaredNewFiles(t *testing.T) {
	d, err := Parse([]byte(example))
	if err != nil {
		t.Fatal(err)
	}
	frozen := map[string]string{Descriptor: example, "go.mod": "module labels\n", "internal/label/label.go": "original"}
	if got := Evaluate(d, frozen, frozen, "baseline"); got.Class != "passed" {
		t.Fatal("new declared file prevented baseline", got)
	}
	candidate := map[string]string{Descriptor: example, "go.mod": "module labels\n", "internal/label/label.go": "normalized", "internal/label/display.go": "display"}
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "passed" {
		t.Fatal("declared new file rejected", got)
	}
	candidate["internal/label/unknown.go"] = "extra"
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class != "infrastructure_failed" {
		t.Fatal("undeclared addition", got)
	}
	delete(candidate, "internal/label/unknown.go")
	delete(candidate, "internal/label/display.go")
	if got := Evaluate(d, frozen, candidate, "candidate"); got.Class == "passed" {
		t.Fatal("missing assertion target", got)
	}
}
